// The two label kinds keep two response schemas (ADR 0017, addendum): the
// outbound Label, whose poNumber and assignmentId stay REQUIRED because
// consumers pin them, and the ReturnLabel of a customer return, which has
// neither. L1 the spec names the right schema per route, L2 the Go views carry
// exactly the properties of their schema (required <-> never omitted), L3 the
// bytes each route really answers satisfy the schema the spec promises and
// never the other kind's.
package http_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
)

const (
	schemaLabel       = "Label"
	schemaReturnLabel = "ReturnLabel"
	voidsPath         = "/labels/{labelRef}/voids"
)

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatalf("load apis/openapi.yaml: %v", err)
	}
	return doc
}

func component(t *testing.T, doc *openapi3.T, name string) *openapi3.Schema {
	t.Helper()
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("apis/openapi.yaml has no schema %s", name)
	}
	return ref.Value
}

// responseSchema is the application/json schema of one documented answer.
func responseSchema(t *testing.T, doc *openapi3.T, path string, status int) *openapi3.SchemaRef {
	t.Helper()
	item := doc.Paths.Find(path)
	if item == nil || item.Post == nil {
		t.Fatalf("apis/openapi.yaml has no POST %s", path)
	}
	resp := item.Post.Responses.Status(status)
	if resp == nil || resp.Value == nil || resp.Value.Content.Get("application/json") == nil {
		t.Fatalf("POST %s documents no application/json %d answer", path, status)
	}
	return resp.Value.Content.Get("application/json").Schema
}

func refName(ref *openapi3.SchemaRef) string {
	return strings.TrimPrefix(ref.Ref, "#/components/schemas/")
}

// L1: POST /labels answers the outbound Label, POST /return-labels its own
// ReturnLabel, and the void route - which voids either kind - a oneOf of the
// two and nothing else.
func TestEachLabelRouteNamesTheSchemaOfItsKind(t *testing.T) {
	doc := loadSpec(t)
	if got := refName(responseSchema(t, doc, "/labels", http.StatusCreated)); got != schemaLabel {
		t.Errorf("POST /labels 201 answers %q, want %s: the forward contract is pinned by consumers", got, schemaLabel)
	}
	if got := refName(responseSchema(t, doc, "/return-labels", http.StatusCreated)); got != schemaReturnLabel {
		t.Errorf("POST /return-labels 201 answers %q, want %s", got, schemaReturnLabel)
	}
	void := responseSchema(t, doc, voidsPath, http.StatusOK)
	var kinds []string
	for _, alt := range void.Value.OneOf {
		kinds = append(kinds, refName(alt))
	}
	if !slices.Equal(kinds, []string{schemaLabel, schemaReturnLabel}) {
		t.Errorf("POST %s 200 answers oneOf %v, want [%s %s]", voidsPath, kinds, schemaLabel, schemaReturnLabel)
	}
}

// L1: the outbound Label still REQUIRES the two fields a consumer reads and
// knows nothing of a return; the ReturnLabel is the mirror image.
func TestLabelSchemasKeepTheirKindApart(t *testing.T) {
	doc := loadSpec(t)
	out, rev := component(t, doc, schemaLabel), component(t, doc, schemaReturnLabel)
	for _, name := range []string{"poNumber", "assignmentId"} {
		if !slices.Contains(out.Required, name) {
			t.Errorf("Label must keep %s required: making it optional breaks every consumer that pins POST /labels", name)
		}
		if rev.Properties[name] != nil {
			t.Errorf("ReturnLabel must not declare %s: a reverse label has no purchase order and no assignment", name)
		}
	}
	for _, name := range []string{"purpose", "returnAuthorizationId"} {
		if out.Properties[name] != nil {
			t.Errorf("Label must not declare %s: it belongs to the ReturnLabel", name)
		}
		if !slices.Contains(rev.Required, name) {
			t.Errorf("ReturnLabel must require %s", name)
		}
	}
}

// L2: a view struct has one JSON field per schema property and nothing else; a
// required property is never omitted, an optional one always may be.
func TestLabelViewsMatchTheirSchemas(t *testing.T) {
	doc := loadSpec(t)
	for name, view := range map[string]any{schemaLabel: views.Label{}, schemaReturnLabel: views.ReturnLabel{}} {
		sch := component(t, doc, name)
		typ := reflect.TypeOf(view)
		seen := map[string]bool{}
		for i := range typ.NumField() {
			tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")
			field, omits := tag[0], slices.Contains(tag[1:], "omitempty")
			seen[field] = true
			if sch.Properties[field] == nil {
				t.Errorf("views.%s.%s (json %q) is not a property of the %s schema", typ.Name(), typ.Field(i).Name, field, name)
				continue
			}
			if required := slices.Contains(sch.Required, field); required == omits {
				t.Errorf("%s.%s: required=%v in the spec but omitempty=%v on views.%s", name, field, required, omits, typ.Name())
			}
		}
		for prop := range sch.Properties {
			if !seen[prop] {
				t.Errorf("%s.%s is in the spec but views.%s never writes it", name, prop, typ.Name())
			}
		}
	}
}

// satisfies reports whether a response body is valid against a schema.
func satisfies(t *testing.T, sch *openapi3.Schema, body string) error {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return sch.VisitJSON(v)
}

// L3: the real answers of the three label routes, issued and voided, against
// the schemas the spec promises for them.
func TestLabelRoutesAnswerTheSchemaTheSpecPromises(t *testing.T) {
	doc := loadSpec(t)
	out, rev := component(t, doc, schemaLabel), component(t, doc, schemaReturnLabel)
	either := responseSchema(t, doc, voidsPath, http.StatusOK).Value
	f := newFixture(t)

	void := func(ref string) reply {
		return f.do(t, "POST", "/labels/"+ref+"/voids", `{"reason":"no longer needed"}`)
	}
	issuedOut := f.do(t, "POST", "/labels", issueBody)
	issuedRev := f.do(t, "POST", "/return-labels", returnLabelBody)
	answers := []struct {
		name       string
		got        reply
		status     int
		kind, twin *openapi3.Schema
	}{
		{"POST /labels", issuedOut, http.StatusCreated, out, rev},
		{"POST /return-labels", issuedRev, http.StatusCreated, rev, out},
		{"void of an outbound label", void(issuedOut.field(t, "labelRef")), http.StatusOK, out, rev},
		{"void of a reverse label", void(issuedRev.field(t, "labelRef")), http.StatusOK, rev, out},
		{"second void of an outbound label", void(issuedOut.field(t, "labelRef")), http.StatusOK, out, rev},
		{"second void of a reverse label", void(issuedRev.field(t, "labelRef")), http.StatusOK, rev, out},
	}
	for _, a := range answers {
		if a.got.status != a.status {
			t.Errorf("%s: status %d, want %d (%s)", a.name, a.got.status, a.status, a.got.body)
			continue
		}
		if err := satisfies(t, a.kind, a.got.body); err != nil {
			t.Errorf("%s does not satisfy the schema of its kind: %v\n%s", a.name, err, a.got.body)
		}
		if satisfies(t, a.twin, a.got.body) == nil {
			t.Errorf("%s also satisfies the other kind's schema: the oneOf of the void route would be ambiguous\n%s", a.name, a.got.body)
		}
		if a.status == http.StatusOK {
			if err := satisfies(t, either, a.got.body); err != nil {
				t.Errorf("%s does not satisfy the oneOf of POST %s: %v", a.name, voidsPath, err)
			}
		}
	}
}

// L3: the outbound answers are the bytes develop shipped - same keys, same
// order - so a consumer of POST /labels sees no difference.
func TestOutboundLabelAnswersKeepTheirShippedKeys(t *testing.T) {
	f := newFixture(t)
	issued := f.do(t, "POST", "/labels", issueBody)
	voided := f.do(t, "POST", "/labels/"+issued.field(t, "labelRef")+"/voids", `{"reason":"gone"}`)
	shipped := []string{"labelRef", "trackingNumber", "carrier", "poNumber", "packageNo", "assignmentId", "orderId", "tier", "status", "issuedAt"}
	for name, c := range map[string]struct {
		body string
		keys []string
	}{
		"issued": {issued.body, shipped},
		"voided": {voided.body, append(slices.Clone(shipped), "voidReason", "voidedAt")},
	} {
		if got := keysInOrder(t, c.body); !slices.Equal(got, c.keys) {
			t.Errorf("%s outbound label keys = %v, want %v", name, got, c.keys)
		}
	}
}

// keysInOrder lists the top-level keys of a JSON object as written.
func keysInOrder(t *testing.T, body string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("body %q is not an object (%v)", body, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		key, ok := tok.(string)
		if !ok {
			t.Fatalf("body %q: key %v is not a string", body, tok)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("body %q: value of %s: %v", body, strconv.Quote(key), err)
		}
	}
	return keys
}
