package journey

import (
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

func at(hours int) time.Time { return t0.Add(time.Duration(hours) * time.Hour) }

func newJourneyIn() NewJourney {
	return NewJourney{
		TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-1", PONumber: "PO-1", AssignmentID: "asg-1",
		OrderID: "ord-1", Tier: shared.TierNextDay,
	}
}

func started(t *testing.T) *Journey {
	t.Helper()
	j, err := Start(newJourneyIn(), t0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return j
}

// path lists the scans that lead a fresh journey to each non-terminal state,
// one hour apart starting at hour 1 (the journey starts at hour 0).
var path = []struct {
	state State
	typ   ScanType
}{
	{StateHandedOver, ScanPickupDeparture},
	{StateInTransit, ScanDeparture},
	{StateAtStation, ScanStationArrival},
	{StateOutForDelivery, ScanOutForDelivery},
	{StateDelivered, ScanDelivered},
}

func scan(id string, typ ScanType, hour int) ScanInput {
	return ScanInput{ID: id, NodeID: "node-" + string(typ), Type: typ, At: at(hour)}
}

// inState returns a journey driven to the given state through RecordScan (or
// through the command that closes it), with the clock at hour+1 afterwards.
func inState(t *testing.T, state State) *Journey {
	t.Helper()
	j := started(t)
	switch state {
	case StateLabelled:
		return j
	case StateVoided:
		mustNoErr(t, j.Void(at(1)))
		return j
	case StateReturned:
		driveTo(t, j, StateAtStation)
		_, err := j.ReturnToSender(at(20), at(21))
		mustNoErr(t, err)
		return j
	case StateLost:
		driveTo(t, j, StateInTransit)
		_, err := j.DeclareLost(at(20), at(21))
		mustNoErr(t, err)
		return j
	}
	driveTo(t, j, state)
	return j
}

func driveTo(t *testing.T, j *Journey, state State) {
	t.Helper()
	for i, step := range path {
		_, err := j.RecordScan(scan("s"+string(step.typ), step.typ, i+1), at(i+1))
		mustNoErr(t, err)
		if step.state == state {
			return
		}
	}
	t.Fatalf("no path to %s", state)
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func eventNames(evs []shared.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventName()
	}
	return out
}

func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
