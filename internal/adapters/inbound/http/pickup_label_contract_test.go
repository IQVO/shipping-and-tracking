package http_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// The pickup refusal of POST /labels, spec and code (ADR 0016): a label to a
// pickup point that the local view does not hold, or holds in any state but
// ACTIVE, is 422 pickup-point-unavailable - a Problem, never a 500.
const (
	slugNoPickupPoint = "pickup-point-unavailable"
	pickupOrder       = "ord-pick"
	pickupPoint       = "node-pnt-07"
	pickupBody        = `{"poNumber":"PO-7","packageNo":1,"assignmentId":"asg-7","orderId":"ord-pick","tier":"NEXT_DAY"}`
)

// pickupFixture is the fixture whose order ord-pick is collected at the
// point; state "" leaves the point out of the view.
func pickupFixture(t *testing.T, state string) *fixture {
	t.Helper()
	f := newFixture(t)
	f.gateway.Put(pickupOrder, shipto.Address{DestinationKind: "PICKUP_POINT", PickupPointID: pickupPoint})
	if state != "" {
		seedPoint(t, f, state)
	}
	return f
}

// seedPoint registers the point in the view, in state.
func seedPoint(t *testing.T, f *fixture, state string) {
	t.Helper()
	if err := f.store.Points().Register(context.Background(), repository.PickupPoint{
		ID: pickupPoint, StationNodeID: "node-ds-07", PostalPrefix: "07095000", State: state,
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}
}

// L1: the refusal is declared where the route answers it.
func TestLabelRouteDeclaresThePickupRefusal(t *testing.T) {
	refusal := loadSpec(t).Paths.Find("/labels").Post.Responses.Status(http.StatusUnprocessableEntity)
	if refusal == nil || refusal.Value == nil || refusal.Value.Description == nil ||
		!strings.Contains(*refusal.Value.Description, slugNoPickupPoint) {
		t.Fatalf("POST /labels must document %s on its 422", slugNoPickupPoint)
	}
	if refusal.Value.Content.Get("application/problem+json") == nil {
		t.Fatal("the 422 of POST /labels answers application/problem+json")
	}
}

// L3: the route answers what the spec declares, for every state of the point
// that is not ACTIVE, and stores no label.
func TestALabelToAnUnavailablePickupPointIsAProblemNotAnInternalError(t *testing.T) {
	problem := component(t, loadSpec(t), "Problem")
	for name, state := range map[string]string{
		"a point the view does not hold": "", "a PROPOSED point": repository.PickupPointProposed,
		"a SUSPENDED point": repository.PickupPointSuspended, "a RETIRED point": repository.PickupPointRetired,
	} {
		t.Run(name, func(t *testing.T) {
			f := pickupFixture(t, state)
			r := f.do(t, "POST", "/labels", pickupBody)
			if r.status != http.StatusUnprocessableEntity || r.ctype != "application/problem+json" || r.field(t, "type") != slugNoPickupPoint {
				t.Fatalf("refusal = %d %s %s, want 422 %s", r.status, r.ctype, r.body, slugNoPickupPoint)
			}
			if err := satisfies(t, problem, r.body); err != nil {
				t.Fatalf("the refusal is not a Problem: %v\n%s", err, r.body)
			}
			if r.field(t, "title") == "" || !strings.Contains(r.field(t, "detail"), pickupPoint) || r.field(t, "instance") != "/labels" {
				t.Fatalf("incomplete problem: %s", r.body)
			}
			if n := f.store.Counts().Labels; n != 0 {
				t.Fatalf("%d labels stored by a refused request, want 0", n)
			}
		})
	}
}

// The refusal depends on the point's state and lifts with it: the same
// request is served once the point is ACTIVE, with the point's own prefix.
func TestThePickupRefusalLiftsWhenThePointIsActivated(t *testing.T) {
	f := pickupFixture(t, repository.PickupPointProposed)
	if r := f.do(t, "POST", "/labels", pickupBody); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("before the activation = %d %s", r.status, r.body)
	}
	if _, err := f.store.Points().SetState(context.Background(), pickupPoint, repository.PickupPointActive); err != nil {
		t.Fatal(err)
	}
	r := f.do(t, "POST", "/labels", pickupBody)
	if r.status != http.StatusCreated {
		t.Fatalf("after the activation = %d %s, want 201", r.status, r.body)
	}
	stored, err := f.store.Labels().Get(context.Background(), label.Ref(r.field(t, "labelRef")))
	if err != nil || stored.DestinationPostalPrefix() != "07095000" {
		t.Fatalf("stored label prefix = %v (%v), want the point's own 07095000", stored, err)
	}
}

// A void of a label whose journey is already closed (delivered) is 409
// journey-closed (journey.Void), which the route must declare.
func TestVoidOfALabelWhoseJourneyIsClosedIsJourneyClosed(t *testing.T) {
	f := newFixture(t)
	issued := f.do(t, "POST", "/labels", issueBody)
	scans := "/journeys/" + issued.field(t, "trackingNumber") + "/scans"
	for i, typ := range []string{"PICKUP_DEPARTURE", "STATION_ARRIVAL", "OUT_FOR_DELIVERY", "DELIVERED"} {
		if got := f.do(t, "POST", scans, scanBody("s"+typ, typ, i+1)); got.status != http.StatusOK {
			t.Fatalf("scan %s = %d %s", typ, got.status, got.body)
		}
	}
	r := f.do(t, "POST", "/labels/"+issued.field(t, "labelRef")+"/voids", `{"reason":"damaged"}`)
	if r.status != http.StatusConflict || r.field(t, "type") != "journey-closed" {
		t.Fatalf("void of a delivered package = %d %s, want 409 journey-closed", r.status, r.body)
	}
	conflict := loadSpec(t).Paths.Find("/labels/{labelRef}/voids").Post.Responses.Status(http.StatusConflict)
	if conflict == nil || conflict.Value == nil || conflict.Value.Description == nil || !strings.Contains(*conflict.Value.Description, "journey-closed") {
		t.Fatal("POST /labels/{labelRef}/voids must document journey-closed on its 409")
	}
}
