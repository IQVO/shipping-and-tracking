// Consumer contract tests against the byte-pinned producer spec.
// testdata/customer-ordering.openapi.yaml is a byte copy of IQVO/customer-ordering
// develop's apis/openapi.yaml; contracts/pinned.json and the pinned-contracts
// CI job keep it equal to the producer. R1 the route the gateway calls is
// declared, R2 the example fixtures satisfy the producer's schemas, R3 the
// fixtures flow through the real gateway call (problem+json answers map to the
// port sentinels), R4 every wire field shipto.Address reads is specified with
// a compatible type. The spec carries no whole-response example for this
// route: the fixtures are derived from its schemas (producer example backfill
// pending).
package customerordering_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/customerordering"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

func loadPinned(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("testdata/customer-ordering.openapi.yaml")
	if err != nil {
		t.Fatalf("load pinned spec: %v", err)
	}
	return doc
}

func readExample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func schema(t *testing.T, doc *openapi3.T, name string) *openapi3.Schema {
	t.Helper()
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("schema %s is not in the pinned spec copy", name)
	}
	return ref.Value
}

// callSites is the REST surface shipping-and-tracking consumes: the model's
// one synchronous call into customer-ordering.
var callSites = []struct{ method, path string }{
	{http.MethodGet, "/orders/{orderId}/ship-to"},
}

// R1: the pinned spec declares every (method, path) the gateway calls, and the
// table names every request the gateway's code builds, so a new call site
// fails here until it is both declared by the producer and pinned.
func TestPinnedSpecCoversEveryCallSite(t *testing.T) {
	src, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(src), "http.NewRequestWithContext"), len(callSites); got != want {
		t.Errorf("gateway.go builds %d requests, the call-site table names %d: update the table and the pin", got, want)
	}
	doc := loadPinned(t)
	for _, c := range callSites {
		item := doc.Paths.Find(c.path)
		if item == nil {
			t.Errorf("pinned spec has no path %s, the gateway calls it", c.path)
			continue
		}
		if item.Operations()[c.method] == nil {
			t.Errorf("pinned spec path %s has no %s operation", c.path, c.method)
		}
	}
}

// R2: every testdata example satisfies the pinned schema of the message it
// stands for: the two 200 answer shapes of the route's oneOf and two
// problem+json answers.
func TestExamplesSatisfyPinnedSpec(t *testing.T) {
	doc := loadPinned(t)
	for name, sch := range map[string]string{
		"ship-to.example.json":            "ShipTo",
		"pickup-destination.example.json": "PickupDestination",
		"not-found.problem.json":          "Problem",
		"unavailable.problem.json":        "Problem",
	} {
		var v any
		if err := json.Unmarshal(readExample(t, name), &v); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err := schema(t, doc, sch).VisitJSON(v); err != nil {
			t.Errorf("%s does not satisfy the pinned %s schema: %v", name, sch, err)
		}
	}
}

// serveFixture answers the gateway's one call with a fixture.
func serveFixture(t *testing.T, status int, contentType, fixture string) *customerordering.Gateway {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(fixture))
	}))
	t.Cleanup(srv.Close)
	g, err := customerordering.New(srv.URL, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// R3: the fixtures flow through the REAL gateway call. The 200 answers decode
// into shipto.Address — an address answer and a pickup answer, the two members
// of the route's oneOf; the problem+json answers map to the port sentinels
// (404 to ports.ErrNotFound, any other status to ports.ErrShipToUnavailable),
// because the gateway maps by HTTP status and never reads the problem body.
func TestGatewayDecodesSpecExamples(t *testing.T) {
	t.Run("ship-to answer", func(t *testing.T) {
		g := serveFixture(t, http.StatusOK, "application/json", string(readExample(t, "ship-to.example.json")))
		a, err := g.GetShipTo(t.Context(), "co-1")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []struct{ name, got, want string }{
			{"name", a.Name, "Ana Example"}, {"line1", a.Line1, "10 Sample Street"}, {"line2", a.Line2, "Apt 4"},
			{"city", a.City, "Sampletown"}, {"region", a.Region, "SP"}, {"postalCode", a.PostalCode, "01310-100"},
			{"countryCode", a.CountryCode, "BR"}, {"phone", a.Phone, "+55 11 5550 0100"},
		} {
			if f.got != f.want {
				t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
			}
		}
		if a.Pickup() {
			t.Error("an address answer is not a pickup")
		}
	})

	t.Run("pickup answer", func(t *testing.T) {
		g := serveFixture(t, http.StatusOK, "application/json", string(readExample(t, "pickup-destination.example.json")))
		a, err := g.GetShipTo(t.Context(), "co-2")
		if err != nil {
			t.Fatal(err)
		}
		if a.DestinationKind != "PICKUP_POINT" || a.PickupPointID != "pp-3701" {
			t.Errorf("destination = kind %q point %q, want PICKUP_POINT pp-3701", a.DestinationKind, a.PickupPointID)
		}
		if !a.Pickup() {
			t.Error("Pickup() must hold for a pickup answer (the address fields stay at their zero value)")
		}
	})

	t.Run("404 problem", func(t *testing.T) {
		g := serveFixture(t, http.StatusNotFound, "application/problem+json", string(readExample(t, "not-found.problem.json")))
		if _, err := g.GetShipTo(t.Context(), "co-404"); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("404 problem+json = %v, want ports.ErrNotFound", err)
		}
	})

	t.Run("503 problem", func(t *testing.T) {
		g := serveFixture(t, http.StatusServiceUnavailable, "application/problem+json", string(readExample(t, "unavailable.problem.json")))
		if _, err := g.GetShipTo(t.Context(), "co-1"); !errors.Is(err, ports.ErrShipToUnavailable) {
			t.Errorf("503 problem+json = %v, want ports.ErrShipToUnavailable", err)
		}
	})
}

// R4: every wire field shipto.Address reads is declared by the pinned schemas
// of the route's answer with a compatible type. The 200 answer is
// oneOf ShipTo | PickupDestination, so the DTO is checked against the union of
// the members' properties, required meaning "required by at least one member":
// on the member that does not declare the field (a pickup answer carries no
// address fields) it decodes to the documented zero value, which the pickup
// subtest above exercises. The problem answers have no DTO to walk — the
// gateway never reads their bodies.
func TestFieldsWeReadAreSpecified(t *testing.T) {
	doc := loadPinned(t)
	merged := &openapi3.Schema{Properties: map[string]*openapi3.SchemaRef{}}
	for _, name := range []string{"ShipTo", "PickupDestination"} {
		props, required := flattenSchema(schema(t, doc, name))
		for k, v := range props {
			merged.Properties[k] = v
		}
		for r := range required {
			merged.Required = append(merged.Required, r)
		}
	}
	checkDTOFields(t, reflect.TypeOf(shipto.Address{}), merged, "ShipTo|PickupDestination")
}

// checkDTOFields walks a response DTO against a pinned schema: every wire
// field must be declared with a compatible type, and every field the schema
// does not require must tolerate absence (pointer, slice or omitempty).
func checkDTOFields(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	props, required := flattenSchema(sch)
	for i := range dt.NumField() {
		f := dt.Field(i)
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue // not a wire field
		}
		p := props[name]
		if p == nil || p.Value == nil {
			t.Errorf("%s.%s: json %q is not declared by the pinned schema", path, f.Name, name)
			continue
		}
		checkWireType(t, f.Type, p.Value, path+"."+name)
		if !required[name] && !toleratesAbsence(f) {
			t.Errorf("%s.%s: optional in the pinned schema, so the DTO must tolerate its absence (pointer, slice or omitempty)", path, f.Name)
		}
	}
}

func flattenSchema(sch *openapi3.Schema) (map[string]*openapi3.SchemaRef, map[string]bool) {
	props := map[string]*openapi3.SchemaRef{}
	required := map[string]bool{}
	var walk func(*openapi3.Schema)
	walk = func(s *openapi3.Schema) {
		if s == nil {
			return
		}
		for n, p := range s.Properties {
			props[n] = p
		}
		for _, r := range s.Required {
			required[r] = true
		}
		for _, b := range s.AllOf {
			walk(b.Value)
		}
	}
	walk(sch)
	return props, required
}

func toleratesAbsence(f reflect.StructField) bool {
	_, _, has := strings.Cut(f.Tag.Get("json"), ",omitempty")
	return has || f.Type.Kind() == reflect.Pointer || f.Type.Kind() == reflect.Slice
}

// scalarWireTypes maps the scalar Go kinds a DTO may carry to the OpenAPI
// type that must declare them.
var scalarWireTypes = map[reflect.Kind]string{
	reflect.String: "string", reflect.Bool: "boolean",
	reflect.Int: "integer", reflect.Int8: "integer", reflect.Int16: "integer", reflect.Int32: "integer", reflect.Int64: "integer",
	reflect.Uint: "integer", reflect.Uint8: "integer", reflect.Uint16: "integer", reflect.Uint32: "integer", reflect.Uint64: "integer",
	reflect.Float32: "number", reflect.Float64: "number",
}

func checkWireType(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	switch dt.Kind() {
	case reflect.Pointer:
		checkWireType(t, dt.Elem(), sch, path)
	case reflect.Slice:
		checkArrayWireType(t, dt.Elem(), sch, path)
	case reflect.Struct:
		checkObjectWireType(t, dt, sch, path)
	default:
		if want, scalar := scalarWireTypes[dt.Kind()]; scalar {
			assertWireType(t, sch, path, want)
		} else {
			t.Errorf("%s: Go kind %s has no wire mapping", path, dt.Kind())
		}
	}
}

func checkArrayWireType(t *testing.T, elem reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	if assertWireType(t, sch, path, "array") && sch.Items != nil && sch.Items.Value != nil {
		checkWireType(t, elem, sch.Items.Value, path+"[]")
	}
}

func checkObjectWireType(t *testing.T, dt reflect.Type, sch *openapi3.Schema, path string) {
	t.Helper()
	if assertWireType(t, sch, path, "object") {
		checkDTOFields(t, dt, sch, path)
	}
}

func assertWireType(t *testing.T, sch *openapi3.Schema, path, want string) bool {
	t.Helper()
	if len(sch.Type.Slice()) > 0 && !sch.Type.Includes(want) {
		t.Errorf("%s: pinned schema type %v, the Go field is %s", path, sch.Type.Slice(), want)
		return false
	}
	return true
}
