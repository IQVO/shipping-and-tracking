package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// pickupRig is the rig with a local pickup-point view (ADR 0016).
func pickupRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.deps.PickupPoints = r.store.Points()
	return r
}

// putPickupPoint stores a point in the local view.
func putPickupPoint(t *testing.T, r *rig, id, state string) {
	t.Helper()
	if err := r.deps.PickupPoints.Upsert(context.Background(), repository.PickupPoint{
		ID: id, StationNodeID: "node-ds-07", PostalPrefix: "07095", State: state,
	}); err != nil {
		t.Fatalf("seed point: %v", err)
	}
}

// pickupOrder makes customer-ordering answer ord-1 with the pickup destination.
func pickupOrder(t *testing.T, r *rig, pointID string) {
	t.Helper()
	r.gateway.Put("ord-1", shipto.Address{DestinationKind: "PICKUP_POINT", PickupPointID: pointID})
}

func TestIssuePickupLabelKeysThePoint(t *testing.T) {
	r := pickupRig(t)
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE")
	pickupOrder(t, r, "node-pnt-07")
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	if err != nil {
		t.Fatalf("IssueLabel pickup: %v", err)
	}
	j := r.journey(t, shared.TrackingNumber(v.TrackingNumber))
	if j.DestinationKind() != "PICKUP_POINT" {
		t.Fatalf("the label served a pickup destination, got %s", j.DestinationKind())
	}
	if j.DestinationKind() != "PICKUP_POINT" || j.PickupPointID() != "node-pnt-07" {
		t.Fatalf("journey destination = %s/%s", j.DestinationKind(), j.PickupPointID())
	}
	if j.HoldUntil().IsZero() {
		t.Fatal("the pickup journey carries a hold deadline")
	}
}

func TestIssuePickupLabelRefusesAnUnavailablePoint(t *testing.T) {
	r := pickupRig(t)
	pickupOrder(t, r, "node-pnt-08") // unknown to the view
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	if err == nil {
		t.Fatal("an unknown point is refused")
	}
	putPickupPoint(t, r, "node-pnt-08", "SUSPENDED")
	if _, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 2)); err == nil {
		t.Fatal("a suspended point is refused")
	}
}

func TestPickupLifecycleDepositCollectSweep(t *testing.T) {
	r := pickupRig(t)
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE")
	pickupOrder(t, r, "node-pnt-07")
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	if err != nil {
		t.Fatal(err)
	}
	tn := shared.TrackingNumber(v.TrackingNumber)

	// the wave's DELIVERED attempt at the point deposits the package
	r.scanTo(t, tn, journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery)
	// the wave's DELIVERED attempt arrives as a consumed last-mile fact: the
	// relay turns it into the deposit scan of a pickup journey (ADR 0016)
	scans := usecases.NewApplyScans(r.deps)
	if _, err := scans.Execute(context.Background(), usecases.ApplyScansCmd{Consumer: "test", EventID: "evt-p1",
		Facts: []usecases.Fact{{TrackingNumber: tn, Type: journey.ScanDelivered, At: hour(6)}}}); err != nil {
		t.Fatalf("drive: %v", err)
	}
	j := r.journey(t, tn)
	if j.State() != journey.StateReadyForCollection {
		t.Fatalf("state = %s, want READY_FOR_COLLECTION (the DELIVERED attempt deposits at a point)", j.State())
	}

	// collecting ends the journey terminally
	in := journey.ScanInput{ID: "collect-1", NodeID: "node-pnt-07", Type: journey.ScanCollected, At: hour(8), ProofCode: "4821"}
	if _, err := j.RecordScan(in, hour(8)); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := r.deps.Journeys.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if j.State() != journey.StateCollected || !j.State().Terminal() {
		t.Fatalf("state = %s, want terminal COLLECTED", j.State())
	}
}

func TestUncollectedSweepReturnsExpiredHolds(t *testing.T) {
	r := pickupRig(t)
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE")
	pickupOrder(t, r, "node-pnt-07")
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	if err != nil {
		t.Fatal(err)
	}
	tn := shared.TrackingNumber(v.TrackingNumber)
	r.scanTo(t, tn, journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery)
	scans := usecases.NewApplyScans(r.deps)
	if _, err := scans.Execute(context.Background(), usecases.ApplyScansCmd{Consumer: "test", EventID: "evt-p2",
		Facts: []usecases.Fact{{TrackingNumber: tn, Type: journey.ScanDelivered, At: hour(6)}}}); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	r.clock.Set(hour(10 * 24)) // past the 7-day hold // past the 7-day hold
	res, err := usecases.NewSweepUncollected(r.deps).Execute(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Returned != 1 {
		t.Fatalf("returned = %d, want 1 (considered %d)", res.Returned, res.Considered)
	}
	j := r.journey(t, tn)
	if j.State() != journey.StateReturned {
		t.Fatalf("state = %s, want RETURNED", j.State())
	}
}
