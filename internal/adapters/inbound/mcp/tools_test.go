package mcp_test

import (
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestGetJourney(t *testing.T) {
	h := newHarness(t)
	seedJourney(t, h)
	got := h.ok(t, "get_journey", map[string]any{"trackingNumber": "RN0123456789ABCDEF"})
	for k, v := range map[string]any{
		"trackingNumber": "RN0123456789ABCDEF", "state": "IN_TRANSIT", "poNumber": "PO-1",
		"assignmentId": "asg-1", "orderId": "ord-1", "tier": "NEXT_DAY",
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if got["handedOverAt"] != "2026-10-24T09:00:00Z" {
		t.Errorf("handedOverAt = %v, want the pickup-departure instant", got["handedOverAt"])
	}
	if _, ok := got["deliveredAt"]; ok {
		t.Errorf("deliveredAt must be absent on an in-transit journey: %v", got)
	}
	scans, _ := got["scans"].([]any)
	if len(scans) != 3 {
		t.Fatalf("scans = %v, want 3 (applied or not)", got["scans"])
	}
	first := scans[0].(map[string]any)
	if first["scanId"] != "scan-1" || first["nodeId"] != "node-A" || first["scanType"] != "PICKUP_DEPARTURE" ||
		first["at"] != "2026-10-24T09:00:00Z" || first["applied"] != true {
		t.Fatalf("first scan = %v", first)
	}
	third := scans[2].(map[string]any)
	if third["scanId"] != "scan-3" || third["applied"] != false {
		t.Fatalf("the out-of-order scan must be stored with applied=false: %v", third)
	}
}

func TestGetJourneyErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_journey", map[string]any{"trackingNumber": "RN0000000000000000"}, "not-found")
}

// A journey with no scan yet is still readable: LABELLED, empty scan list.
func TestGetJourneyLabelledWithNoScan(t *testing.T) {
	h := newHarness(t)
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-2", PONumber: "PO-2",
		AssignmentID: "asg-2", OrderID: "ord-2", Tier: shared.TierStandard,
	}, t0)
	must(t, err)
	h.saveJourney(t, j)
	got := h.ok(t, "get_journey", map[string]any{"trackingNumber": "RN0123456789ABCDEF"})
	scans, _ := got["scans"].([]any)
	if got["state"] != "LABELLED" || got["tier"] != "STANDARD" || scans == nil || len(scans) != 0 {
		t.Fatalf("a fresh journey = %v, want LABELLED with a non-nil empty scan list", got)
	}
}

// The read tool is side-effect free: no outbox row, no stored change.
func TestReadsEmitNothing(t *testing.T) {
	h := newHarness(t)
	seedJourney(t, h)
	before := h.store.Counts()
	h.ok(t, "get_journey", map[string]any{"trackingNumber": "RN0123456789ABCDEF"})
	failWith(t, h.session, "get_journey", map[string]any{"trackingNumber": "RN0000000000000000"}, "not-found")
	if after := h.store.Counts(); after != before {
		t.Fatalf("store %v -> %v: a read tool changed stored state", before, after)
	}
}
