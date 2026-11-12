package a2a

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// outForDeliveryRepo stores one OUT_FOR_DELIVERY journey; with eta it also
// carries the day-of window assigned at the OUT_FOR_DELIVERY scan.
func outForDeliveryRepo(t *testing.T, eta bool) Deps {
	t.Helper()
	repo := memory.NewStore().Journeys()
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: seededTracking, LabelRef: "lbl-1", PONumber: "PO-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
	}, t0)
	must(t, err)
	must(t, repo.Save(t.Context(), j))
	for i, typ := range []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery} {
		at := t0.Add(time.Duration(i+1) * time.Hour)
		_, err = j.RecordScan(journey.ScanInput{ID: "scan-" + string(typ), NodeID: "node-A", Type: typ, At: at}, at)
		must(t, err)
		must(t, repo.Save(t.Context(), j))
	}
	if eta {
		_, err = j.AssignEta(t0.Add(6*time.Hour), t0.Add(8*time.Hour), t0.Add(4*time.Hour), t0.Add(5*time.Hour))
		must(t, err)
		must(t, repo.Save(t.Context(), j))
	}
	return depsOver(repo)
}

func TestWireGetJourneyCarriesTheEtaWindowWhileOutForDelivery(t *testing.T) {
	w := newWire(t, outForDeliveryRepo(t, true))
	env, raw, _ := w.track(t, seededTracking)
	j := env.Data.Journey
	if env.Problem != nil || j == nil || j.State != "OUT_FOR_DELIVERY" {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.Contains(raw, `"etaStart":"2026-10-24T14:00:00Z"`) || !strings.Contains(raw, `"etaEnd":"2026-10-24T16:00:00Z"`) {
		t.Fatalf("raw journey = %s", raw)
	}
}

func TestWireGetJourneyOmitsTheEtaWindowWhenThereIsNone(t *testing.T) {
	w := newWire(t, outForDeliveryRepo(t, false))
	_, raw, _ := w.track(t, seededTracking)
	if strings.Contains(raw, "eta") {
		t.Fatalf("raw journey = %s, want no ETA fields", raw)
	}
}
