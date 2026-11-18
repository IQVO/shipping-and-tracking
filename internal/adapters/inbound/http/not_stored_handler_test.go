package http_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// markSpy stands where the Idempotency-Key middleware stands and hands every
// handler a writer it can mark as not to be stored, exactly as the
// middleware's own capture does (DoNotCache). It records the mark of the last
// request; it stores nothing, so it needs no database.
type markSpy struct{ marked bool }

type markableWriter struct {
	http.ResponseWriter
	spy *markSpy
}

func (w markableWriter) DoNotCache() { w.spy.marked = true }

func (s *markSpy) middleware(string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.marked = false
			next.ServeHTTP(markableWriter{w, s}, r)
		})
	}
}

// The handlers mark ONE refusal of the label routes as not to be stored under
// the Idempotency-Key: destination-prefix-unavailable, which the same request
// passes once the node fact has arrived (ADR 0018). Every other answer of
// these routes - the success, the final refusals, a dependency failure - is
// left unmarked, to the middleware's own rule.
func TestOnlyTheDestinationRefusalIsMarkedNotToBeStored(t *testing.T) {
	spy := &markSpy{}
	f := newFixtureWith(t, nil, spy.middleware)
	if err := f.store.NodeDestinations().Upsert(context.Background(), repository.NodeDestination{NodeID: prefixlessNode}); err != nil {
		t.Fatal(err)
	}
	byNode := returnBody("ret-1", `,"destinationNodeId":"`+returnsNode+`"`)
	steps := []struct {
		name, path, body string
		before           func()
		status           int
		slug             string
		marked           bool
	}{
		{"a node this context does not know", "/return-labels", byNode, nil, 409, slugNoPrefix, true},
		{"a node without a usable prefix", "/return-labels", returnBody("ret-2", `,"destinationNodeId":"`+prefixlessNode+`"`), nil, 409, slugNoPrefix, true},
		{"both destination forms", "/return-labels", returnBody("ret-3", `,"destinationNodeId":"`+returnsNode+`","destinationPostalPrefix":"01310"`), nil, 422, "validation-failed", false},
		{"customer-ordering down", "/return-labels", returnBody("ret-4", `,"destinationPostalPrefix":"01310"`), func() { f.gateway.Fail(true) }, 503, "ship-to-unavailable", false},
		{"the same request once the node fact arrived", "/return-labels", byNode, func() {
			f.gateway.Fail(false)
			if err := f.store.NodeDestinations().Upsert(context.Background(),
				repository.NodeDestination{NodeID: returnsNode, PostalPrefix: returnsNodePrefix}); err != nil {
				t.Fatal(err)
			}
		}, 201, "", false},
		{"the label already issued", "/return-labels", byNode, nil, 409, "label-already-issued", false},
		{"an outbound label", "/labels", issueBody, nil, 201, "", false},
		{"the outbound label already issued", "/labels", issueBody, nil, 409, "label-already-issued", false},
	}
	for _, s := range steps {
		if s.before != nil {
			s.before()
		}
		r := f.do(t, "POST", s.path, s.body)
		if r.status != s.status || (s.slug != "" && r.field(t, "type") != s.slug) {
			t.Fatalf("%s: got %d %s, want %d %s", s.name, r.status, r.body, s.status, s.slug)
		}
		if spy.marked != s.marked {
			t.Errorf("%s (%d %s): marked not-to-store = %v, want %v", s.name, s.status, s.slug, spy.marked, s.marked)
		}
	}
}
