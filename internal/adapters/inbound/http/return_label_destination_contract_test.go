// POST /return-labels names the return destination in exactly one of two ways
// (ADR 0018): destinationNodeId, whose prefix this context derives from its
// own node view, or the deprecated destinationPostalPrefix. L1 the spec says
// so (a oneOf of two required sets, the old property deprecated, the refusal
// declared), L3 the route accepts exactly the bodies the request schema
// accepts and answers what the spec promises for each.
package http_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

const (
	schemaReturnRequest = "IssueReturnLabelRequest"
	propNode            = "destinationNodeId"
	propPrefix          = "destinationPostalPrefix"
	slugNoPrefix        = "destination-prefix-unavailable"
	// returnsNode is known to the fixture's node view; its prefix differs from
	// the ship-to prefix of ord-1 (01310) and from anything a body carries.
	returnsNode       = "node-rc-01"
	returnsNodePrefix = "07750"
	// prefixlessNode is known too, but its postal code left no prefix.
	prefixlessNode = "node-rc-02"
)

// nodeFixture is the fixture with the two nodes above in the node view.
func nodeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	for id, prefix := range map[string]string{returnsNode: returnsNodePrefix, prefixlessNode: ""} {
		if err := f.store.NodeDestinations().Upsert(context.Background(), repository.NodeDestination{NodeID: id, PostalPrefix: prefix}); err != nil {
			t.Fatalf("seed node %s: %v", id, err)
		}
	}
	return f
}

// returnBody is a POST /return-labels body of authorization auth whose
// destination part is written out by the caller.
func returnBody(auth, destination string) string {
	return `{"returnAuthorizationId":"` + auth + `","orderId":"ord-1","packageNo":1` + destination + `}`
}

// L1: the request schema states the exactly-one rule and the deprecation.
func TestReturnLabelRequestSchemaNamesTheDestinationExactlyOnce(t *testing.T) {
	sch := component(t, loadSpec(t), schemaReturnRequest)
	for _, name := range []string{propNode, propPrefix} {
		if sch.Properties[name] == nil {
			t.Fatalf("%s must declare %s", schemaReturnRequest, name)
		}
		if slices.Contains(sch.Required, name) {
			t.Errorf("%s must not require %s outright: the oneOf decides", schemaReturnRequest, name)
		}
	}
	if !sch.Properties[propPrefix].Value.Deprecated {
		t.Errorf("%s is the form being retired: it must be marked deprecated", propPrefix)
	}
	if sch.Properties[propNode].Value.Deprecated {
		t.Errorf("%s is the form to send: it must not be deprecated", propNode)
	}
	var sets [][]string
	for _, alt := range sch.OneOf {
		sets = append(sets, alt.Value.Required)
	}
	if len(sets) != 2 || !slices.Equal(sets[0], []string{propNode}) || !slices.Equal(sets[1], []string{propPrefix}) {
		t.Errorf("%s oneOf requires %v, want [[%s] [%s]]", schemaReturnRequest, sets, propNode, propPrefix)
	}
}

// L1: the refusal is declared where the route answers it.
func TestReturnLabelRouteDeclaresTheDestinationRefusal(t *testing.T) {
	conflict := loadSpec(t).Paths.Find("/return-labels").Post.Responses.Status(http.StatusConflict)
	if conflict == nil || conflict.Value == nil || conflict.Value.Description == nil ||
		!strings.Contains(*conflict.Value.Description, slugNoPrefix) {
		t.Fatalf("POST /return-labels must document %s on its 409", slugNoPrefix)
	}
	if conflict.Value.Content.Get("application/problem+json") == nil {
		t.Fatal("the 409 of POST /return-labels answers application/problem+json")
	}
}

// L3: the request schema and the route agree on every body: what the schema
// rejects is 422 validation-failed, what it accepts is never that.
func TestReturnLabelRouteAcceptsExactlyWhatItsRequestSchemaAccepts(t *testing.T) {
	sch := component(t, loadSpec(t), schemaReturnRequest)
	long := strings.Repeat("n", 65)
	cases := []struct {
		name, destination string
		valid             bool
		status            int
		slug              string
	}{
		{"the node alone", `,"destinationNodeId":"` + returnsNode + `"`, true, 201, ""},
		{"the deprecated prefix alone", `,"destinationPostalPrefix":"01310"`, true, 201, ""},
		{"a node this context does not know", `,"destinationNodeId":"node-nowhere"`, true, 409, slugNoPrefix},
		{"a node without a usable prefix", `,"destinationNodeId":"` + prefixlessNode + `"`, true, 409, slugNoPrefix},
		{"both", `,"destinationNodeId":"` + returnsNode + `","destinationPostalPrefix":"01310"`, false, 422, "validation-failed"},
		{"neither", ``, false, 422, "validation-failed"},
		{"an empty node id", `,"destinationNodeId":""`, false, 422, "validation-failed"},
		{"a node id of 65 characters", `,"destinationNodeId":"` + long + `"`, false, 422, "validation-failed"},
		{"a null node id beside a prefix", `,"destinationNodeId":null,"destinationPostalPrefix":"01310"`, false, 422, "validation-failed"},
		{"a null prefix beside a node id", `,"destinationNodeId":"` + returnsNode + `","destinationPostalPrefix":null`, false, 422, "validation-failed"},
		{"a node id that is not a string", `,"destinationNodeId":7`, false, 422, "validation-failed"},
		{"an empty prefix beside a node id", `,"destinationNodeId":"` + returnsNode + `","destinationPostalPrefix":""`, false, 422, "validation-failed"},
		{"a prefix outside A-Z 0-9", `,"destinationPostalPrefix":"013 1"`, false, 422, "validation-failed"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := nodeFixture(t)
			body := returnBody("ret-"+string(rune('a'+i)), c.destination)
			if got := satisfies(t, sch, body) == nil; got != c.valid {
				t.Fatalf("the %s schema accepts=%v, want %v: %s", schemaReturnRequest, got, c.valid, body)
			}
			r := f.do(t, "POST", "/return-labels", body)
			if r.status != c.status {
				t.Fatalf("status = %d, want %d: %s", r.status, c.status, r.body)
			}
			if c.slug != "" && (r.ctype != "application/problem+json" || r.field(t, "type") != c.slug) {
				t.Fatalf("answer = %s %s, want problem %s", r.ctype, r.body, c.slug)
			}
			if n := len(f.store.Outbox().Events()); (n == 1) != (c.status == 201) {
				t.Fatalf("outbox holds %d events after a %d", n, c.status)
			}
		})
	}
}

// L3: the answers of both forms are ReturnLabels, and the prefix on the stored
// label is the node's own when the node was named, the caller's otherwise.
func TestReturnLabelPrefixComesFromTheNodeOrFromTheCaller(t *testing.T) {
	rev := component(t, loadSpec(t), schemaReturnLabel)
	f := nodeFixture(t)
	for _, c := range []struct{ name, auth, destination, want string }{
		{"by node", "ret-1", `,"destinationNodeId":"` + returnsNode + `"`, returnsNodePrefix},
		{"by the deprecated prefix", "ret-2", `,"destinationPostalPrefix":"01310"`, "01310"},
	} {
		r := f.do(t, "POST", "/return-labels", returnBody(c.auth, c.destination))
		if r.status != http.StatusCreated {
			t.Fatalf("%s: status %d %s", c.name, r.status, r.body)
		}
		if err := satisfies(t, rev, r.body); err != nil {
			t.Errorf("%s: the answer is not a ReturnLabel: %v\n%s", c.name, err, r.body)
		}
		if strings.Contains(r.body, "Prefix") || strings.Contains(r.body, "NodeId") {
			t.Errorf("%s: the answer carries no destination field: %s", c.name, r.body)
		}
		l, err := f.store.Labels().Get(context.Background(), label.Ref(r.field(t, "labelRef")))
		if err != nil {
			t.Fatal(err)
		}
		if l.DestinationPostalPrefix() != c.want {
			t.Errorf("%s: label prefix = %q, want %q", c.name, l.DestinationPostalPrefix(), c.want)
		}
	}
}

// The refusal is a complete RFC 9457 problem and is state-dependent: the same
// body is served once the node fact has arrived. (This fixture has no
// Idempotency-Key store; what a stored refusal does to a retry under the same
// key is the integration test of idempotency_return_node_integration_test.go.)
func TestReturnLabelDestinationRefusalIsAProblemAndLiftsWithTheNodeFact(t *testing.T) {
	f := newFixture(t)
	body := returnBody("ret-1", `,"destinationNodeId":"`+returnsNode+`"`)
	r := f.do(t, "POST", "/return-labels", body)
	if r.status != http.StatusConflict || r.ctype != "application/problem+json" || r.field(t, "type") != slugNoPrefix {
		t.Fatalf("refusal = %d %s %s", r.status, r.ctype, r.body)
	}
	if err := satisfies(t, component(t, loadSpec(t), "Problem"), r.body); err != nil {
		t.Fatalf("the refusal is not a Problem: %v\n%s", err, r.body)
	}
	if r.field(t, "title") == "" || !strings.Contains(r.field(t, "detail"), returnsNode) || r.field(t, "instance") != "/return-labels" {
		t.Fatalf("incomplete problem: %s", r.body)
	}
	if err := f.store.NodeDestinations().Upsert(context.Background(),
		repository.NodeDestination{NodeID: returnsNode, PostalPrefix: returnsNodePrefix}); err != nil {
		t.Fatal(err)
	}
	if again := f.do(t, "POST", "/return-labels", body); again.status != http.StatusCreated {
		t.Fatalf("after the node fact: %d %s", again.status, again.body)
	}
}
