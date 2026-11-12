package views_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func outForDelivery(t *testing.T) *journey.Journey {
	t.Helper()
	j, err := journey.Start(journey.NewJourney{TrackingNumber: "RN1", LabelRef: "lbl-1", PONumber: "PO-1", AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierTwoDay}, t0)
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery} {
		at := t0.Add(time.Duration(i+1) * time.Hour)
		if _, err := j.RecordScan(journey.ScanInput{ID: "s" + string(rune('a'+i)), NodeID: "n1", Type: typ, At: at}, at); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func TestJourneyViewShowsTheEtaWindowWhileItIsCurrent(t *testing.T) {
	j := outForDelivery(t)
	start, end := t0.Add(6*time.Hour), t0.Add(8*time.Hour)
	if _, err := j.AssignEta(start, end, t0.Add(4*time.Hour), t0.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	v := views.JourneyFrom(j)
	if v.EtaStart == nil || v.EtaEnd == nil || !v.EtaStart.Equal(start) || !v.EtaEnd.Equal(end) {
		t.Fatalf("view = %+v", v)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"etaStart":"2026-10-23T14:00:00Z","etaEnd":"2026-10-23T16:00:00Z"`) {
		t.Fatalf("JSON = %s", b)
	}
}

func TestJourneyViewOmitsTheEtaWindowWhenItIsNotCurrent(t *testing.T) {
	j := outForDelivery(t)
	if v := views.JourneyFrom(j); v.EtaStart != nil || v.EtaEnd != nil {
		t.Fatalf("no window stored, view = %+v", v)
	}
	if _, err := j.AssignEta(t0.Add(6*time.Hour), t0.Add(8*time.Hour), t0.Add(4*time.Hour), t0.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := j.RecordScan(journey.ScanInput{ID: "failed", NodeID: "n1", Type: journey.ScanDeliveryFailed, At: t0.Add(9 * time.Hour)}, t0.Add(9*time.Hour)); err != nil {
		t.Fatal(err)
	}
	v := views.JourneyFrom(j)
	b, _ := json.Marshal(v)
	if v.EtaStart != nil || v.EtaEnd != nil || strings.Contains(string(b), "eta") {
		t.Fatalf("a failed attempt hides the window: %s", b)
	}
}
