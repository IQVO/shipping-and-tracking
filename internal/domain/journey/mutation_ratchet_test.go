package journey

import (
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// reversed returns a reversed copy, so every ordering case is asserted for
// both input orders: a comparator that only works for one of them fails.
func reversed(in []CarrierOption) []CarrierOption {
	out := make([]CarrierOption, len(in))
	for i, o := range in {
		out[len(in)-1-i] = o
	}
	return out
}

// TestChooseCarrierEachSortKeyDecidesAlone pins every level of the ADR 0015
// ordering on its own: cost ascending beats everything, then transit
// descending, then carrierId ascending, and a full tie keeps the input order.
func TestChooseCarrierEachSortKeyDecidesAlone(t *testing.T) {
	cases := []struct {
		name     string
		opts     []CarrierOption
		wantID   string
		wantNode string
	}{
		{"cheaper wins over a later transit", []CarrierOption{
			{CarrierID: "crr-a", HandoffNodeID: "n1", CostPerPackageCents: 500, TransitMinutes: 9000},
			{CarrierID: "crr-b", HandoffNodeID: "n2", CostPerPackageCents: 300, TransitMinutes: 100},
		}, "crr-b", "n2"},
		{"cheaper wins over a smaller id", []CarrierOption{
			{CarrierID: "crr-a", HandoffNodeID: "n1", CostPerPackageCents: 400, TransitMinutes: 100},
			{CarrierID: "crr-z", HandoffNodeID: "n2", CostPerPackageCents: 399, TransitMinutes: 100},
		}, "crr-z", "n2"},
		{"equal cost: the later transit wins", []CarrierOption{
			{CarrierID: "crr-a", HandoffNodeID: "n1", CostPerPackageCents: 300, TransitMinutes: 100},
			{CarrierID: "crr-b", HandoffNodeID: "n2", CostPerPackageCents: 300, TransitMinutes: 200},
		}, "crr-b", "n2"},
		{"equal cost: the later transit wins over a smaller id", []CarrierOption{
			{CarrierID: "crr-a", HandoffNodeID: "n1", CostPerPackageCents: 300, TransitMinutes: 199},
			{CarrierID: "crr-z", HandoffNodeID: "n2", CostPerPackageCents: 300, TransitMinutes: 200},
		}, "crr-z", "n2"},
		{"equal cost and transit: the smaller id wins", []CarrierOption{
			{CarrierID: "crr-b", HandoffNodeID: "n1", CostPerPackageCents: 300, TransitMinutes: 200},
			{CarrierID: "crr-a", HandoffNodeID: "n2", CostPerPackageCents: 300, TransitMinutes: 200},
		}, "crr-a", "n2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for label, opts := range map[string][]CarrierOption{"as given": c.opts, "reversed": reversed(c.opts)} {
				got, ok := ChooseCarrier(opts)
				if !ok || got.CarrierID != c.wantID || got.HandoffNodeID != c.wantNode {
					t.Fatalf("%s: choose = %+v %v, want %s at %s", label, got, ok, c.wantID, c.wantNode)
				}
			}
		})
	}
}

// TestChooseCarrierFullTieKeepsInputOrderAndDoesNotMutateInput: two options
// equal on all three keys are ordered by input position (stable), and the
// caller's slice is left untouched.
func TestChooseCarrierFullTieKeepsInputOrderAndDoesNotMutateInput(t *testing.T) {
	opts := []CarrierOption{
		{CarrierID: "crr-a", HandoffNodeID: "first", CostPerPackageCents: 300, TransitMinutes: 200},
		{CarrierID: "crr-a", HandoffNodeID: "second", CostPerPackageCents: 300, TransitMinutes: 200},
	}
	want := append([]CarrierOption(nil), opts...)
	got, ok := ChooseCarrier(opts)
	if !ok || got.HandoffNodeID != "first" {
		t.Fatalf("a full tie must keep the first option, got %+v %v", got, ok)
	}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("ChooseCarrier reordered the caller's slice: %+v", opts)
	}
}

// returnedUncollected drives a pickup journey to READY_FOR_COLLECTION and
// closes it as an uncollected return (ADR 0016).
func returnedUncollected(t *testing.T) (*Journey, []shared.Event, int64) {
	t.Helper()
	j := inState(t, StateReadyForCollection)
	before := j.Version()
	evs, err := j.ReturnUncollected(at(40), at(41))
	mustNoErr(t, err)
	return j, evs, before
}

func TestReturnUncollectedClosesAReadyForCollectionJourney(t *testing.T) {
	j, evs, before := returnedUncollected(t)
	if j.State() != StateReturned || j.ClosedAt() != at(41) || j.Version() != before+1 {
		t.Fatalf("state=%s closedAt=%v v%d (before v%d)", j.State(), j.ClosedAt(), j.Version(), before)
	}
	ev, ok := evs[0].(PackageReturned)
	if len(evs) != 1 || !ok {
		t.Fatalf("want one PackageReturned, got %v", evs)
	}
	if ev.Reason != "UNCOLLECTED" || ev.OrderID != "ord-1" ||
		ev.TrackingNumber != "RN0123456789ABCDEF" || !ev.ReturnedAt.Equal(at(40)) ||
		ev.OccurredAt() != at(41) || ev.AggregateVersion() != j.Version() {
		t.Fatalf("event = %+v", ev)
	}
}

func TestReturnUncollectedOnlyFromReadyForCollection(t *testing.T) {
	for _, st := range []State{StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery} {
		j := inState(t, st)
		before := j.Version()
		evs, err := j.ReturnUncollected(at(40), at(41))
		assertRefusedUnchanged(t, j, evs, err, st, before)
	}
	for _, st := range []State{StateDelivered, StateCollected} {
		closed := inState(t, st)
		if _, err := closed.ReturnUncollected(at(40), at(41)); !errors.Is(err, ErrJourneyClosed) {
			t.Fatalf("%s: want ErrJourneyClosed, got %v", st, err)
		}
	}
}

// TestRestorePickupStates: a pickup journey in READY_FOR_COLLECTION, COLLECTED
// and returned-uncollected restores; a returned pickup journey must have
// been closed from READY_FOR_COLLECTION, not from the station.
func TestRestorePickupStates(t *testing.T) {
	for _, st := range []State{StateReadyForCollection, StateCollected} {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			got, err := Restore(j.Snapshot())
			mustNoErr(t, err)
			if got.State() != st || !reflect.DeepEqual(got.Snapshot(), j.Snapshot()) {
				t.Fatalf("round trip differs: %s\n%+v\n%+v", got.State(), got.Snapshot(), j.Snapshot())
			}
		})
	}
	t.Run("returned uncollected", func(t *testing.T) {
		j, _, _ := returnedUncollected(t)
		got, err := Restore(j.Snapshot())
		mustNoErr(t, err)
		if got.State() != StateReturned || !reflect.DeepEqual(got.Snapshot(), j.Snapshot()) {
			t.Fatalf("round trip differs: %s", got.State())
		}
	})
	t.Run("returned pickup journey closed from the station", func(t *testing.T) {
		// A pickup journey whose applied scans end AT_STATION cannot be a
		// returned-uncollected one: that closes from READY_FOR_COLLECTION.
		pj := startedPickup(t)
		driveTo(t, pj, StateAtStation)
		s := pj.Snapshot()
		s.State, s.ClosedAt = StateReturned, at(9)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Fatalf("want ErrInvalidSnapshot, got %v", err)
		}
	})
}

// TestRestoreDestinationKind: an empty kind (a journey saved before ADR 0016)
// and the two known kinds restore; any other kind is corrupt.
func TestRestoreDestinationKind(t *testing.T) {
	s := inState(t, StateInTransit).Snapshot()
	for _, kind := range []string{"", destinationShipTo} {
		s.DestinationKind = kind
		if _, err := Restore(s); err != nil {
			t.Fatalf("kind %q must restore: %v", kind, err)
		}
	}
	s.DestinationKind = "WAREHOUSE"
	if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("an unknown kind must be refused, got %v", err)
	}
}

// TestRestoreReplaysFailedDeliveryByCarrierChoice: the replay sends a failed
// delivery back to the carrier's custody only for a journey that carries a
// carrier choice, and back to the station otherwise.
func TestRestoreReplaysFailedDeliveryByCarrierChoice(t *testing.T) {
	carrierJourney := inState(t, StateWithCarrier)
	_, err := carrierJourney.RecordScan(scan("ofd", ScanOutForDelivery, 6), at(6))
	mustNoErr(t, err)
	_, err = carrierJourney.RecordScan(scan("fail", ScanDeliveryFailed, 7), at(7))
	mustNoErr(t, err)
	if carrierJourney.State() != StateWithCarrier {
		t.Fatalf("fixture: state = %s", carrierJourney.State())
	}
	got, err := Restore(carrierJourney.Snapshot())
	mustNoErr(t, err)
	if got.State() != StateWithCarrier || !reflect.DeepEqual(got.Snapshot(), carrierJourney.Snapshot()) {
		t.Fatalf("carrier journey after a failed delivery must restore WITH_CARRIER, got %s", got.State())
	}

	plain := inState(t, StateOutForDelivery)
	_, err = plain.RecordScan(scan("fail", ScanDeliveryFailed, 7), at(7))
	mustNoErr(t, err)
	if plain.State() != StateAtStation {
		t.Fatalf("fixture: state = %s", plain.State())
	}
	got, err = Restore(plain.Snapshot())
	mustNoErr(t, err)
	if got.State() != StateAtStation {
		t.Fatalf("a journey without a carrier choice returns to the station, got %s", got.State())
	}
	// And the carrier flag is what decides: a plain journey claiming
	// WITH_CARRIER after the same scans is unreachable.
	s := plain.Snapshot()
	s.State = StateWithCarrier
	if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("WITH_CARRIER without a carrier choice must be refused, got %v", err)
	}
}
