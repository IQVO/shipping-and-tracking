package http_test

import (
	"context"
	"net/http"
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

// markSpy stands where the Idempotency-Key middleware stands and hands every
// handler a writer it can mark with a replay class, exactly as the
// middleware's own capture does (MarkReplay). It records the class of the last
// request; it stores nothing, so it needs no database.
type markSpy struct{ marked bool }

type markableWriter struct {
	http.ResponseWriter
	spy *markSpy
}

// MarkReplay records whether the refusal is RETRYABLE, the class that releases
// the key.
func (w markableWriter) MarkReplay(c inbound.ReplayClass) {
	w.spy.marked = c == inbound.ReplayRetryable
}

func (s *markSpy) middleware(string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.marked = false
			next.ServeHTTP(markableWriter{w, s}, r)
		})
	}
}

// The handlers tell the writer the replay class of each answer (ADR 0020).
// The refusals the same request passes later are RETRYABLE, so the key is
// released: destination-prefix-unavailable, once the node fact has arrived
// (ADR 0018), pickup-point-unavailable, once the point is ACTIVE (ADR 0016),
// validation-failed (a body that names an order customer-ordering does not
// know yet) and ship-to-unavailable (a dependency that is not ready). The
// success and the refusal that is final (label-already-issued) are not.
func TestOnlyTheRefusalsARequestCanPassLaterAreMarkedRetryable(t *testing.T) {
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
		{"both destination forms", "/return-labels", returnBody("ret-3", `,"destinationNodeId":"`+returnsNode+`","destinationPostalPrefix":"01310"`), nil, 422, "validation-failed", true},
		{"customer-ordering down", "/return-labels", returnBody("ret-4", `,"destinationPostalPrefix":"01310"`), func() { f.gateway.Fail(true) }, 503, "ship-to-unavailable", true},
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
		{"a pickup point this context does not know", "/labels", pickupBody, func() {
			f.gateway.Put(pickupOrder, shipto.Address{DestinationKind: "PICKUP_POINT", PickupPointID: pickupPoint})
		}, 422, slugNoPickupPoint, true},
		{"a pickup point that is not ACTIVE yet", "/labels", pickupBody, func() {
			seedPoint(t, f, repository.PickupPointProposed)
		}, 422, slugNoPickupPoint, true},
		{"the same request once the point is ACTIVE", "/labels", pickupBody, func() {
			if _, err := f.store.Points().SetState(context.Background(), pickupPoint, repository.PickupPointActive); err != nil {
				t.Fatal(err)
			}
		}, 201, "", false},
		{"the pickup label already issued", "/labels", pickupBody, nil, 409, "label-already-issued", false},
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
			t.Errorf("%s (%d %s): marked RETRYABLE = %v, want %v", s.name, s.status, s.slug, spy.marked, s.marked)
		}
	}
}
