package usecases_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// areaRig wires the AreaModeView double into a rig.
func areaRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.deps.AreaModes = r.store.Areas()
	return r
}

func seedCarrierArea(t *testing.T, r *rig, prefix string, version int64) {
	t.Helper()
	stored, err := r.deps.AreaModes.Apply(context.Background(), repository.AreaMode{
		PostalPrefix: prefix, DeliveryMode: repository.DeliveryModeCarrierHandoff, Version: version,
		Carriers: []journey.CarrierOption{
			{CarrierID: "crr-saver", HandoffNodeID: "node-sc-03", CostPerPackageCents: 380, TransitMinutes: 2880},
			{CarrierID: "crr-fast", HandoffNodeID: "node-sc-04", CostPerPackageCents: 380, TransitMinutes: 1440},
		},
	})
	if err != nil || !stored {
		t.Fatalf("seed area: stored=%v err=%v", stored, err)
	}
}

func TestStationArrivalChoosesTheCarrierDeterministically(t *testing.T) {
	r := areaRig(t)
	seedCarrierArea(t, r, "01310", 4) // the sample address's prefix
	tn := r.issued(t)
	r.scanTo(t, tn, toAtStation...)
	j := r.journey(t, tn)
	if !j.CarrierChosen() {
		t.Fatal("the station arrival of a carrier-area package must store the choice")
	}
	// cost tie 380=380, then transit DESCENDING: the 2880-minute option wins.
	if j.ChosenCarrierID() != "crr-saver" || j.HandoffNodeID() != "node-sc-03" || j.AreaVersion() != 4 {
		t.Fatalf("choice = %s %s %d", j.ChosenCarrierID(), j.HandoffNodeID(), j.AreaVersion())
	}
}

func TestStationArrivalOnAShipToAreaChoosesNothing(t *testing.T) {
	r := areaRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toAtStation...)
	if r.journey(t, tn).CarrierChosen() {
		t.Fatal("no area row: the pre-C4 world, no choice")
	}
	// and a manual handoff scan on that journey is 422 (the ADR 0015 guard)
	_, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), usecases.RecordScanCmd{
		TrackingNumber: tn, ScanID: "h1", NodeID: "node-sc-03", Type: journey.ScanCarrierHandoff, At: hour(9),
	})
	wantErr(t, err, shared.ErrValidation)
}

func TestApplyAreaAssignmentGuardsTheVersion(t *testing.T) {
	r := areaRig(t)
	ctx := context.Background()
	apply := usecases.NewApplyAreaAssignment(r.deps)
	res, err := apply.Execute(ctx, usecases.ApplyAreaCmd{Consumer: "node-topology", EventID: "e1",
		Area: repository.AreaMode{PostalPrefix: "01310", DeliveryMode: repository.DeliveryModeCarrierHandoff, Version: 2,
			Carriers: []journey.CarrierOption{{CarrierID: "crr-a", HandoffNodeID: "n1", CostPerPackageCents: 100, TransitMinutes: 60}}}})
	noErr(t, err)
	if !res.Stored {
		t.Fatal("the first assignment is stored")
	}
	res, err = apply.Execute(ctx, usecases.ApplyAreaCmd{Consumer: "node-topology", EventID: "e2",
		Area: repository.AreaMode{PostalPrefix: "01310", DeliveryMode: repository.DeliveryModeNetworkRoute, Version: 1}})
	noErr(t, err)
	if !res.Stale {
		t.Fatal("an older version is stale, not stored")
	}
	res, err = apply.Execute(ctx, usecases.ApplyAreaCmd{Consumer: "node-topology", EventID: "e1",
		Area: repository.AreaMode{PostalPrefix: "01310", DeliveryMode: repository.DeliveryModeNetworkRoute, Version: 9}})
	noErr(t, err)
	if !res.Skipped {
		t.Fatal("a replayed CloudEvents id is skipped by the inbox")
	}
}

func carrierCmd(tn shared.TrackingNumber, status string, at int) usecases.CarrierEventCmd {
	return usecases.CarrierEventCmd{CarrierID: "crr-saver", CarrierEventID: "evt-1", TrackingNumber: tn, Status: status, At: hour(at)}
}

func TestCarrierEventLifecycleThroughTheWebhook(t *testing.T) {
	r := areaRig(t)
	seedCarrierArea(t, r, "01310", 1)
	tn := r.issued(t)
	r.scanTo(t, tn, toAtStation...)
	uc := usecases.NewReportCarrierEvent(r.deps)
	r.clock.Set(hour(10)) // the carrier reports after the whole in-network path

	// a PICKED_UP before any handoff, on a journey not at the station, is stored not applied
	stored := carrierCmd(tn, "PICKED_UP", 1)
	stored.CarrierEventID = "evt-0"
	if _, err := uc.Execute(context.Background(), stored); err == nil {
		t.Fatal("expected a refusal for an out-of-order report")
	}

	v, err := uc.Execute(context.Background(), carrierCmd(tn, "PICKED_UP", 4))
	noErr(t, err)
	if v.State != string(journey.StateWithCarrier) {
		t.Fatalf("state = %s, want WITH_CARRIER", v.State)
	}
	if names := eventNames(r.store.Outbox().Events()); !strings.HasSuffix(names, "PackageHandedToCarrier") {
		t.Fatalf("events = %s, want ...PackageHandedToCarrier", names)
	}

	// a re-sent report is a duplicate scan (the scanId is carrierId:carrierEventId)
	_, err = uc.Execute(context.Background(), carrierCmd(tn, "PICKED_UP", 4))
	wantErr(t, err, journey.ErrDuplicateScan)

	// IN_TRANSIT is acknowledged, no scan
	before := len(r.journey(t, tn).Scans())
	v, err = uc.Execute(context.Background(), carrierCmd(tn, "IN_TRANSIT", 5))
	noErr(t, err)
	if v.State != string(journey.StateWithCarrier) || len(r.journey(t, tn).Scans()) != before {
		t.Fatal("IN_TRANSIT must not add a scan")
	}

	// a second PICKED_UP with a fresh event id is a no-op on a WITH_CARRIER journey
	cmd := carrierCmd(tn, "PICKED_UP", 5)
	cmd.CarrierEventID = "evt-2"
	v, err = uc.Execute(context.Background(), cmd)
	noErr(t, err)
	if v.State != string(journey.StateWithCarrier) {
		t.Fatal("a second PICKED_UP changes nothing")
	}

	cmd = carrierCmd(tn, "OUT_FOR_DELIVERY", 6)
	cmd.CarrierEventID = "evt-4"
	v, err = uc.Execute(context.Background(), cmd)
	noErr(t, err)
	if v.State != string(journey.StateOutForDelivery) {
		t.Fatalf("state = %s, want OUT_FOR_DELIVERY", v.State)
	}
	cmd = carrierCmd(tn, "DELIVERY_FAILED", 7)
	cmd.CarrierEventID = "evt-5"
	v, err = uc.Execute(context.Background(), cmd)
	noErr(t, err)
	if v.State != string(journey.StateWithCarrier) {
		t.Fatalf("a failed carrier delivery returns to carrier custody, got %s", v.State)
	}
	cmd = carrierCmd(tn, "OUT_FOR_DELIVERY", 8) // the retry after the failed attempt
	cmd.CarrierEventID = "evt-6"
	v, err = uc.Execute(context.Background(), cmd)
	noErr(t, err)
	if v.State != string(journey.StateOutForDelivery) {
		t.Fatalf("the retry is out for delivery again, got %s", v.State)
	}
	cmd = carrierCmd(tn, "DELIVERED", 9)
	cmd.CarrierEventID = "evt-3"
	v, err = uc.Execute(context.Background(), cmd)
	noErr(t, err)
	if v.State != string(journey.StateDelivered) {
		t.Fatalf("state = %s, want DELIVERED", v.State)
	}
	if names := eventNames(r.store.Outbox().Events()); !strings.HasPrefix(names, "LabelIssued") {
		t.Fatalf("sanity: the outbox starts at the label, got %s", names)
	}
}

func TestCarrierEventValidation(t *testing.T) {
	r := areaRig(t)
	tn := r.issued(t)
	r.clock.Set(hour(10))
	uc := usecases.NewReportCarrierEvent(r.deps)
	cases := map[string]func(c *usecases.CarrierEventCmd){
		"bad status":    func(c *usecases.CarrierEventCmd) { c.Status = "LOST" },
		"blank carrier": func(c *usecases.CarrierEventCmd) { c.CarrierID = "" },
		"blank event":   func(c *usecases.CarrierEventCmd) { c.CarrierEventID = "" },
		"no at":         func(c *usecases.CarrierEventCmd) { c.At = time.Time{} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := carrierCmd(tn, "PICKED_UP", 4)
			mut(&cmd)
			_, err := uc.Execute(context.Background(), cmd)
			wantErr(t, err, shared.ErrValidation)
		})
	}
	cmd := carrierCmd(tn, "PICKED_UP", 4)
	cmd.At = hour(2000)
	if _, err := uc.Execute(context.Background(), cmd); err == nil {
		t.Fatal("a far-future at is refused beyond the clock-skew allowance")
	}
	cmd = carrierCmd(shared.TrackingNumber("RNDOESNOTEXIST0001"), "PICKED_UP", 4)
	_, err := uc.Execute(context.Background(), cmd)
	wantErr(t, err, ports.ErrNotFound)
}
