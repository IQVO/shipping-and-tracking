package journey

import (
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestChooseCarrierSortsByCostTransitThenId(t *testing.T) {
	opts := []CarrierOption{
		{CarrierID: "crr-b", HandoffNodeID: "node-h", CostPerPackageCents: 380, TransitMinutes: 2880},
		{CarrierID: "crr-a", HandoffNodeID: "node-h", CostPerPackageCents: 380, TransitMinutes: 2880},
		{CarrierID: "crr-c", HandoffNodeID: "node-x", CostPerPackageCents: 300, TransitMinutes: 5000},
		{CarrierID: "crr-d", HandoffNodeID: "node-x", CostPerPackageCents: 300, TransitMinutes: 4000},
		{CarrierID: "crr-e", HandoffNodeID: "node-y", CostPerPackageCents: 900, TransitMinutes: 60},
	}
	// cost ascending first (crr-c/crr-d at 300 beat crr-a/crr-b at 380), then
	// transit DESCENDING within one cost (crr-c's later window wins), then the
	// lexicographically smallest carrierId.
	got, ok := ChooseCarrier(opts)
	if !ok || got.CarrierID != "crr-c" {
		t.Fatalf("choose = %+v %v, want crr-c", got, ok)
	}
	if only, ok := ChooseCarrier(opts[4:5]); !ok || only.CarrierID != "crr-e" {
		t.Fatalf("a single option is the degenerate case, got %+v %v", only, ok)
	}
	if _, ok := ChooseCarrier(nil); ok {
		t.Fatal("no options, no choice")
	}
}

func TestNoteCarrierChoiceAndRoundTrip(t *testing.T) {
	j, _ := chosenAndHandedOver(t)
	restored, err := Restore(j.Snapshot())
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.CarrierChosen() || restored.State() != StateWithCarrier {
		t.Fatalf("round trip lost the carrier choice or state: %s", restored.State())
	}
}

// chosenAndHandedOver drives one journey to WITH_CARRIER through the carrier
// choice and the handoff scan, proving the choice fields and the raised events.
func chosenAndHandedOver(t *testing.T) (*Journey, Result) {
	t.Helper()
	j := inState(t, StateAtStation)
	if j.CarrierChosen() {
		t.Fatal("a station arrival without a carrier area chooses nothing")
	}
	if err := j.NoteCarrierChoice("crr-1", "node-handoff", 4); err != nil {
		t.Fatalf("note: %v", err)
	}
	if !j.CarrierChosen() || j.ChosenCarrierID() != "crr-1" || j.HandoffNodeID() != "node-handoff" || j.AreaVersion() != 4 {
		t.Fatalf("choice = %s %s %d", j.ChosenCarrierID(), j.HandoffNodeID(), j.AreaVersion())
	}
	r, err := j.RecordScan(ScanInput{ID: "h1", NodeID: "node-handoff", Type: ScanCarrierHandoff, At: at(5), DestinationPostalPrefix: "01310"}, at(5))
	if err != nil || !r.Changed {
		t.Fatalf("handoff scan: %v", err)
	}
	if j.State() != StateWithCarrier {
		t.Fatalf("state = %s, want WITH_CARRIER", j.State())
	}
	names := eventNames(r.Events)
	if len(names) != 2 || names[0] != "PackageScanned" || names[1] != "PackageHandedToCarrier" {
		t.Fatalf("events = %v, want PackageScanned + PackageHandedToCarrier", names)
	}
	return j, r
}

func TestCarrierChoiceFieldsAreAllOrNothing(t *testing.T) {
	j := inState(t, StateAtStation)
	if err := j.NoteCarrierChoice("crr-1", "", 1); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("half a choice must be refused, got %v", err)
	}
	if _, err := Restore(Snapshot{TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-1", PONumber: "PO-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay, State: StateAtStation,
		StartedAt: t0, Version: 2, ChosenCarrierID: "crr-1"}); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a snapshot with a lone carrierId must be refused, got %v", err)
	}
}

func TestCarrierFailureReturnsToCarrierCustody(t *testing.T) {
	j := inState(t, StateWithCarrier)
	if _, err := j.RecordScan(scan("ofd", ScanOutForDelivery, 6), at(6)); err != nil {
		t.Fatalf("out for delivery from WITH_CARRIER: %v", err)
	}
	if _, err := j.RecordScan(scan("fail", ScanDeliveryFailed, 7), at(7)); err != nil {
		t.Fatalf("failed delivery: %v", err)
	}
	if j.State() != StateWithCarrier {
		t.Fatalf("a carrier journey re-attempts from carrier custody, got %s", j.State())
	}
}

func TestHandoffScanWithoutChoiceIsRefused(t *testing.T) {
	j := inState(t, StateAtStation)
	if _, err := j.RecordScan(scan("h1", ScanCarrierHandoff, 5), at(5)); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	if j.State() != StateAtStation {
		t.Fatalf("nothing moved, got %s", j.State())
	}
}

func TestWithCarrierIsSilenceWatched(t *testing.T) {
	j := inState(t, StateWithCarrier)
	if SilenceWindow(j.Tier(), StateWithCarrier) == 0 {
		t.Fatal("WITH_CARRIER must be watched with the tier window")
	}
	if !j.Silent(at(4).Add(SilenceWindow(j.Tier(), StateWithCarrier))) {
		t.Fatal("a quiet WITH_CARRIER journey goes silent")
	}
}
