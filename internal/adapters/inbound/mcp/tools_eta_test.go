package mcp_test

import (
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// outForDelivery stores a journey driven to OUT_FOR_DELIVERY at hour 5 of t0.
func (h *harness) outForDelivery(t *testing.T) *journey.Journey {
	t.Helper()
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-1", PONumber: "PO-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
	}, t0)
	must(t, err)
	h.saveJourney(t, j)
	for i, typ := range []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery} {
		at := t0.Add(time.Duration(i+1) * time.Hour)
		_, err = j.RecordScan(journey.ScanInput{ID: "scan-" + string(typ), NodeID: "node-A", Type: typ, At: at}, at)
		must(t, err)
		h.saveJourney(t, j)
	}
	return j
}

func TestGetJourneyShowsTheEtaWindowWhileOutForDelivery(t *testing.T) {
	h := newHarness(t)
	j := h.outForDelivery(t)
	_, err := j.AssignEta(t0.Add(6*time.Hour), t0.Add(8*time.Hour), t0.Add(4*time.Hour), t0.Add(5*time.Hour))
	must(t, err)
	h.saveJourney(t, j)
	got := h.ok(t, "get_journey", map[string]any{"trackingNumber": "RN0123456789ABCDEF"})
	if got["etaStart"] != "2026-10-24T14:00:00Z" || got["etaEnd"] != "2026-10-24T16:00:00Z" {
		t.Fatalf("eta = %v .. %v", got["etaStart"], got["etaEnd"])
	}
}

func TestGetJourneyOmitsTheEtaWindowWhenNoneIsCurrent(t *testing.T) {
	h := newHarness(t)
	j := h.outForDelivery(t)
	got := h.ok(t, "get_journey", map[string]any{"trackingNumber": "RN0123456789ABCDEF"})
	if _, ok := got["etaStart"]; ok {
		t.Fatalf("no window yet: %v", got)
	}
	_, err := j.AssignEta(t0.Add(6*time.Hour), t0.Add(8*time.Hour), t0.Add(4*time.Hour), t0.Add(5*time.Hour))
	must(t, err)
	h.saveJourney(t, j)
	_, err = j.RecordScan(journey.ScanInput{ID: "failed", NodeID: "node-A", Type: journey.ScanDeliveryFailed, At: t0.Add(9 * time.Hour)}, t0.Add(9*time.Hour))
	must(t, err)
	h.saveJourney(t, j)
	got = h.ok(t, "get_journey", map[string]any{"trackingNumber": "RN0123456789ABCDEF"})
	if _, ok := got["etaStart"]; ok {
		t.Fatalf("a failed attempt hides the window: %v", got)
	}
	if _, ok := got["etaEnd"]; ok {
		t.Fatalf("a failed attempt hides the window: %v", got)
	}
}
