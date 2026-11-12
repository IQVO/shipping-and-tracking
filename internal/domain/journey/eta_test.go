package journey

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// The OUT_FOR_DELIVERY scan of driveTo is at hour 4; windows are minutes after it.
func etaWin(fromMin, toMin int) (time.Time, time.Time) {
	return at(4).Add(time.Duration(fromMin) * time.Minute), at(4).Add(time.Duration(toMin) * time.Minute)
}

func TestAssignEtaStoresTheWindowAndRaisesTheEvent(t *testing.T) {
	j := inState(t, StateOutForDelivery)
	before := j.Version()
	s, e := etaWin(60, 180)
	evs, err := j.AssignEta(s, e, at(4), at(5))
	mustNoErr(t, err)
	if len(evs) != 1 || evs[0].EventName() != "PackageEtaWindowAssigned" {
		t.Fatalf("events = %v", eventNames(evs))
	}
	checkEtaEvent(t, j, evs[0].(PackageEtaWindowAssigned), s, e, before)
	if j.State() != StateOutForDelivery || j.Version() != before+1 {
		t.Fatalf("state=%s version=%d", j.State(), j.Version())
	}
	if !j.EtaStart().Equal(s) || !j.EtaEnd().Equal(e) || !j.EtaAssignedAt().Equal(at(4)) {
		t.Fatalf("eta=%v..%v at %v", j.EtaStart(), j.EtaEnd(), j.EtaAssignedAt())
	}
}

func checkEtaEvent(t *testing.T, j *Journey, got PackageEtaWindowAssigned, s, e time.Time, before int64) {
	t.Helper()
	if got.TrackingNumber != j.TrackingNumber() || got.AssignmentID != "asg-1" || got.OrderID != "ord-1" {
		t.Fatalf("event identity = %+v", got)
	}
	if !got.EtaStart.Equal(s) || !got.EtaEnd.Equal(e) || !got.AssignedAt.Equal(at(4)) {
		t.Fatalf("event window = %+v", got)
	}
	if got.AggregateVersion() != before+1 || !got.OccurredAt().Equal(at(5)) {
		t.Fatalf("event header = %+v", got.Header)
	}
}

func TestAssignEtaRefusesAStaleOrRepeatedAssignment(t *testing.T) {
	for name, assignedAt := range map[string]time.Time{"older": at(3), "equal": at(4)} {
		t.Run(name, func(t *testing.T) {
			j := inState(t, StateOutForDelivery)
			s, e := etaWin(60, 180)
			_, err := j.AssignEta(s, e, at(4), at(5))
			mustNoErr(t, err)
			v := j.Version()
			s2, e2 := etaWin(120, 240)
			evs, err := j.AssignEta(s2, e2, assignedAt, at(6))
			if !errors.Is(err, ErrEtaStale) || len(evs) != 0 {
				t.Fatalf("err = %v events = %v", err, evs)
			}
			if j.Version() != v || !j.EtaStart().Equal(s) {
				t.Fatal("a stale ETA must change nothing")
			}
		})
	}
}

func TestAssignEtaReplacesWithANewerWindow(t *testing.T) {
	j := inState(t, StateOutForDelivery)
	s, e := etaWin(60, 180)
	_, err := j.AssignEta(s, e, at(4), at(5))
	mustNoErr(t, err)
	s2, e2 := etaWin(300, 420)
	_, err = j.AssignEta(s2, e2, at(5), at(6))
	mustNoErr(t, err)
	if !j.EtaStart().Equal(s2) || !j.EtaEnd().Equal(e2) || !j.EtaAssignedAt().Equal(at(5)) {
		t.Fatalf("window = %v..%v at %v", j.EtaStart(), j.EtaEnd(), j.EtaAssignedAt())
	}
}

func TestAssignEtaRefusesATerminalJourney(t *testing.T) {
	for _, st := range []State{StateDelivered, StateReturned, StateLost, StateVoided} {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			s, e := etaWin(60, 180)
			evs, err := j.AssignEta(s, e, at(30), at(31))
			if !errors.Is(err, ErrJourneyClosed) || len(evs) != 0 || !j.EtaAssignedAt().IsZero() {
				t.Fatalf("err = %v events = %v", err, evs)
			}
		})
	}
}

func TestAssignEtaStoresInAnyOpenState(t *testing.T) {
	for _, st := range []State{StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery} {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			s, e := etaWin(60, 180)
			_, err := j.AssignEta(s, e, at(10), at(11))
			mustNoErr(t, err)
			if j.State() != st {
				t.Fatalf("AssignEta moved the state to %s", j.State())
			}
		})
	}
}

func TestAssignEtaValidatesItsInput(t *testing.T) {
	s, e := etaWin(60, 180)
	cases := map[string][3]time.Time{
		"zero start":      {{}, e, at(4)},
		"zero end":        {s, {}, at(4)},
		"start after end": {e, s, at(4)},
		"start equals":    {s, s, at(4)},
		"zero assignedAt": {s, e, {}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			j := inState(t, StateOutForDelivery)
			_, err := j.AssignEta(c[0], c[1], c[2], at(5))
			if !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestEtaCurrentInEveryState(t *testing.T) {
	for _, st := range allStates {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			s, e := etaWin(60, 180)
			// assignedAt equal to the OUT_FOR_DELIVERY scan (hour 4) and after any
			// later scan: the only state that shows it is OUT_FOR_DELIVERY.
			if st.Terminal() {
				j.etaStart, j.etaEnd, j.etaAssignedAt = s, e, at(4)
			} else {
				_, err := j.AssignEta(s, e, at(4), at(5))
				mustNoErr(t, err)
			}
			if got, want := j.EtaCurrent(), st == StateOutForDelivery; got != want {
				t.Fatalf("EtaCurrent = %v, want %v", got, want)
			}
		})
	}
}

func TestEtaCurrentComparesTheAssignmentWithTheLastScan(t *testing.T) {
	cases := map[string]struct {
		assignedAt time.Time
		want       bool
	}{
		"assigned after the scan":  {at(5), true},
		"assigned at the scan":     {at(4), true},
		"assigned before the scan": {at(3), false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			j := inState(t, StateOutForDelivery) // lastScanAt = hour 4
			s, e := etaWin(60, 180)
			_, err := j.AssignEta(s, e, c.assignedAt, at(6))
			mustNoErr(t, err)
			if j.EtaCurrent() != c.want {
				t.Fatalf("EtaCurrent = %v, want %v", j.EtaCurrent(), c.want)
			}
		})
	}
}

func TestEtaCurrentIsFalseWithoutAWindowAndAfterAFailedAttempt(t *testing.T) {
	j := inState(t, StateOutForDelivery)
	if j.EtaCurrent() {
		t.Fatal("a journey with no window has no current ETA")
	}
	s, e := etaWin(60, 180)
	_, err := j.AssignEta(s, e, at(4), at(5))
	mustNoErr(t, err)
	_, err = j.RecordScan(scan("failed", ScanDeliveryFailed, 8), at(8))
	mustNoErr(t, err)
	if j.State() != StateAtStation || j.EtaCurrent() {
		t.Fatalf("state=%s current=%v: a failed attempt hides the window", j.State(), j.EtaCurrent())
	}
	_, err = j.RecordScan(scan("again", ScanOutForDelivery, 30), at(30))
	mustNoErr(t, err)
	if j.EtaCurrent() {
		t.Fatal("yesterday's window must be stale once a newer OUT_FOR_DELIVERY scan exists")
	}
}

func TestEtaFieldsRoundTripThroughSnapshotAndRestore(t *testing.T) {
	j := inState(t, StateOutForDelivery)
	s, e := etaWin(60, 180)
	_, err := j.AssignEta(s, e, at(4), at(5))
	mustNoErr(t, err)
	snap := j.Snapshot()
	if !snap.EtaStart.Equal(s) || !snap.EtaEnd.Equal(e) || !snap.EtaAssignedAt.Equal(at(4)) {
		t.Fatalf("snapshot = %+v", snap)
	}
	got, err := Restore(snap)
	mustNoErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), snap) || !got.EtaCurrent() {
		t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), snap)
	}
}

func TestRestoreChecksTheEtaInvariant(t *testing.T) {
	s, e := etaWin(60, 180)
	cases := map[string]func(*Snapshot){
		"start only":      func(sn *Snapshot) { sn.EtaStart = s },
		"end only":        func(sn *Snapshot) { sn.EtaEnd = e },
		"assigned only":   func(sn *Snapshot) { sn.EtaAssignedAt = at(4) },
		"no assignedAt":   func(sn *Snapshot) { sn.EtaStart, sn.EtaEnd = s, e },
		"start after end": func(sn *Snapshot) { sn.EtaStart, sn.EtaEnd, sn.EtaAssignedAt = e, s, at(4) },
		"start equals":    func(sn *Snapshot) { sn.EtaStart, sn.EtaEnd, sn.EtaAssignedAt = s, s, at(4) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sn := inState(t, StateOutForDelivery).Snapshot()
			mutate(&sn)
			if _, err := Restore(sn); !errors.Is(err, shared.ErrInvalidSnapshot) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
