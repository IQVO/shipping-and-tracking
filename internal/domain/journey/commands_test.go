package journey

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestRaiseExceptionInEveryOpenStateKeepsTheState(t *testing.T) {
	for _, st := range openStates {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			before := j.Version()
			evs, err := j.RaiseException(at(40))
			mustNoErr(t, err)
			if j.State() != st || j.Version() != before+1 || j.LastExceptionAt() != at(40) {
				t.Fatalf("state=%s v%d exceptionAt=%v", j.State(), j.Version(), j.LastExceptionAt())
			}
			assertExceptionEvent(t, evs, before+1)
		})
	}
}

func assertExceptionEvent(t *testing.T, evs []shared.Event, version int64) {
	t.Helper()
	ev, ok := evs[0].(PackageExceptionRaised)
	if len(evs) != 1 || !ok {
		t.Fatalf("want one PackageExceptionRaised, got %v", evs)
	}
	if ev.Reason != "NO_SCAN" || ev.AssignmentID != "asg-1" || ev.OrderID != "ord-1" || !ev.RaisedAt.Equal(at(40)) ||
		ev.TrackingNumber != "RN0123456789ABCDEF" || ev.AggregateVersion() != version {
		t.Fatalf("event = %+v", ev)
	}
}

func TestEveryCommandRefusesATerminalJourney(t *testing.T) {
	for _, st := range []State{StateDelivered, StateReturned, StateLost, StateVoided} {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			before := j.Version()
			_, e1 := j.RaiseException(at(50))
			_, e2 := j.ReturnToSender(at(50), at(51))
			_, e3 := j.DeclareLost(at(50), at(51))
			e4 := j.Void(at(51))
			for i, err := range []error{e1, e2, e3, e4} {
				if !errors.Is(err, ErrJourneyClosed) {
					t.Errorf("command %d: want ErrJourneyClosed, got %v", i, err)
				}
			}
			if j.Version() != before || j.State() != st {
				t.Fatal("a closed journey must not change")
			}
		})
	}
}

func assertRefusedUnchanged(t *testing.T, j *Journey, evs []shared.Event, err error, st State, before int64) {
	t.Helper()
	if !errors.Is(err, shared.ErrIllegalTransition) || evs != nil || j.State() != st || j.Version() != before {
		t.Errorf("%s: want illegal transition unchanged, got %v %v %s", st, evs, err, j.State())
	}
}

func TestReturnToSenderOnlyFromAtStation(t *testing.T) {
	for _, st := range []State{StateLabelled, StateHandedOver, StateInTransit, StateOutForDelivery} {
		j := inState(t, st)
		before := j.Version()
		evs, err := j.ReturnToSender(at(40), at(41))
		assertRefusedUnchanged(t, j, evs, err, st, before)
	}
	j := inState(t, StateAtStation)
	before := j.Version()
	evs, err := j.ReturnToSender(at(40).In(time.FixedZone("x", 7200)), at(41))
	mustNoErr(t, err)
	if j.State() != StateReturned || j.ClosedAt() != at(41) || j.Version() != before+1 {
		t.Fatalf("state=%s closedAt=%v v%d", j.State(), j.ClosedAt(), j.Version())
	}
	assertReturnedEvent(t, evs, j.Version())
}

func assertReturnedEvent(t *testing.T, evs []shared.Event, version int64) {
	t.Helper()
	ev, ok := evs[0].(PackageReturned)
	if len(evs) != 1 || !ok {
		t.Fatalf("want one PackageReturned, got %v", evs)
	}
	if ev.OrderID != "ord-1" || ev.TrackingNumber != "RN0123456789ABCDEF" || !ev.ReturnedAt.Equal(at(40)) ||
		ev.ReturnedAt.Location() != time.UTC || ev.OccurredAt() != at(41) || ev.AggregateVersion() != version ||
		ev.EventName() != "PackageReturned" || ev.Entity() != "journey" || ev.SubjectID() != "RN0123456789ABCDEF" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestDeclareLostOnlyFromInTransit(t *testing.T) {
	for _, st := range []State{StateLabelled, StateHandedOver, StateAtStation, StateOutForDelivery} {
		j := inState(t, st)
		before := j.Version()
		evs, err := j.DeclareLost(at(40), at(41))
		assertRefusedUnchanged(t, j, evs, err, st, before)
	}
	j := inState(t, StateInTransit)
	before := j.Version()
	evs, err := j.DeclareLost(at(40).In(time.FixedZone("x", -7200)), at(41))
	mustNoErr(t, err)
	if j.State() != StateLost || j.ClosedAt() != at(41) || j.Version() != before+1 {
		t.Fatalf("state=%s closedAt=%v v%d", j.State(), j.ClosedAt(), j.Version())
	}
	assertLostEvent(t, evs, j.Version())
}

func assertLostEvent(t *testing.T, evs []shared.Event, version int64) {
	t.Helper()
	ev, ok := evs[0].(PackageLost)
	if len(evs) != 1 || !ok {
		t.Fatalf("want one PackageLost, got %v", evs)
	}
	if ev.OrderID != "ord-1" || ev.TrackingNumber != "RN0123456789ABCDEF" || !ev.DeclaredAt.Equal(at(40)) ||
		ev.DeclaredAt.Location() != time.UTC || ev.OccurredAt() != at(41) || ev.AggregateVersion() != version ||
		ev.EventName() != "PackageLost" || ev.Entity() != "journey" || ev.SubjectID() != "RN0123456789ABCDEF" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestVoidOnlyWhileLabelled(t *testing.T) {
	j := inState(t, StateLabelled)
	before := j.Version()
	mustNoErr(t, j.Void(at(5)))
	if j.State() != StateVoided || j.ClosedAt() != at(5) || j.Version() != before+1 {
		t.Fatalf("state=%s closedAt=%v v%d", j.State(), j.ClosedAt(), j.Version())
	}
	for _, st := range []State{StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery} {
		j := inState(t, st)
		before := j.Version()
		if err := j.Void(at(50)); !errors.Is(err, ErrVoidAfterHandover) || j.State() != st || j.Version() != before {
			t.Errorf("%s: want ErrVoidAfterHandover unchanged, got %v", st, err)
		}
	}
}

func TestStateHelpers(t *testing.T) {
	all := []State{StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery,
		StateDelivered, StateReturned, StateLost, StateVoided}
	terminal := map[State]bool{StateDelivered: true, StateReturned: true, StateLost: true, StateVoided: true}
	for _, s := range all {
		if !s.Valid() || s.Terminal() != terminal[s] {
			t.Errorf("%s: valid=%v terminal=%v", s, s.Valid(), s.Terminal())
		}
	}
	if State("").Valid() || State("DONE").Valid() || State("DONE").Terminal() {
		t.Error("unknown states are invalid and not terminal")
	}
	if len(AllScanTypes()) != 8 || ScanType("").Valid() {
		t.Error("eight scan types, and the empty one is invalid")
	}
}

func TestNextIsUndefinedForUnknownScanTypes(t *testing.T) {
	if to, ok := next("TELEPORT", StateLabelled); ok || to != "" {
		t.Fatalf("got %s %v", to, ok)
	}
}
