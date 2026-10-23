package journey

import (
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestSilenceWindows(t *testing.T) {
	watched := []State{StateHandedOver, StateInTransit, StateAtStation}
	byTier := map[shared.Tier]time.Duration{
		shared.TierSameDay: 6 * time.Hour, shared.TierNextDay: 24 * time.Hour,
		shared.TierTwoDay: 48 * time.Hour, shared.TierStandard: 72 * time.Hour,
	}
	for tier, want := range byTier {
		for _, st := range watched {
			if got := SilenceWindow(tier, st); got != want {
				t.Errorf("SilenceWindow(%s, %s) = %s, want %s", tier, st, got, want)
			}
		}
		if got := SilenceWindow(tier, StateOutForDelivery); got != 12*time.Hour || OutForDeliveryWindowHours != 12 {
			t.Errorf("out for delivery window = %s", got)
		}
		for _, st := range []State{StateLabelled, StateDelivered, StateReturned, StateLost, StateVoided} {
			if got := SilenceWindow(tier, st); got != 0 {
				t.Errorf("SilenceWindow(%s, %s) = %s, want 0 (not watched)", tier, st, got)
			}
		}
	}
	if SilenceWindow("", StateInTransit) != 0 {
		t.Error("an unknown tier has no window")
	}
	if MinSilenceWindow() != byTier[shared.TierSameDay] {
		t.Error("MinSilenceWindow must be the shortest tier window")
	}
}

func TestLostAfterExceptionIsFortyEightHours(t *testing.T) {
	if LostAfterException() != 48*time.Hour {
		t.Fatalf("got %s", LostAfterException())
	}
}

func TestSilentAtTheWindowBoundary(t *testing.T) {
	j := inState(t, StateInTransit) // NEXT_DAY, last scan at hour 2
	last := j.LastScanAt()
	if j.Silent(last.Add(24*time.Hour - time.Nanosecond)) {
		t.Error("one nanosecond inside the window is not silent")
	}
	if !j.Silent(last.Add(24 * time.Hour)) {
		t.Error("exactly at the window is silent")
	}
	if !j.Silent(last.Add(25 * time.Hour)) {
		t.Error("past the window is silent")
	}
}

func TestSilentOnlyForWatchedStates(t *testing.T) {
	far := at(1000)
	for _, st := range []State{StateLabelled, StateDelivered, StateReturned, StateLost, StateVoided} {
		if inState(t, st).Silent(far) {
			t.Errorf("%s must never be silent", st)
		}
	}
	for _, st := range []State{StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery} {
		if !inState(t, st).Silent(far) {
			t.Errorf("%s must be silent after a long time", st)
		}
	}
}

func TestOneExceptionPerSilenceUntilANewScanArrives(t *testing.T) {
	j := inState(t, StateInTransit)
	now := j.LastScanAt().Add(30 * time.Hour)
	if !j.Silent(now) {
		t.Fatal("must be silent")
	}
	_, err := j.RaiseException(now)
	mustNoErr(t, err)
	if j.Silent(now.Add(100 * time.Hour)) {
		t.Fatal("no second exception for the same silence")
	}
	// A new applied scan after the exception ends the silence.
	_, err = j.RecordScan(scan("fresh", ScanTransitArrival, 40), now.Add(time.Hour))
	mustNoErr(t, err)
	if j.Silent(j.LastScanAt().Add(23 * time.Hour)) {
		t.Fatal("a fresh scan restarts the window")
	}
	if !j.Silent(j.LastScanAt().Add(24 * time.Hour)) {
		t.Fatal("and a new silence may raise a new exception")
	}
}

func TestExceptionAtTheSameInstantAsTheLastScanStillCountsAsRaised(t *testing.T) {
	j := inState(t, StateInTransit)
	_, err := j.RaiseException(j.LastScanAt())
	mustNoErr(t, err)
	if j.Silent(at(1000)) {
		t.Fatal("an exception at the instant of the last scan answers that silence")
	}
}

func TestLostAfterAnUnansweredException(t *testing.T) {
	j := inState(t, StateInTransit)
	raised := j.LastScanAt().Add(30 * time.Hour)
	if j.Lost(raised.Add(1000 * time.Hour)) {
		t.Fatal("no exception raised yet: not lost")
	}
	_, err := j.RaiseException(raised)
	mustNoErr(t, err)
	if j.Lost(raised.Add(LostAfterException() - time.Nanosecond)) {
		t.Error("one nanosecond before 48 hours is not lost")
	}
	if !j.Lost(raised.Add(LostAfterException())) {
		t.Error("exactly 48 hours after the exception is lost")
	}
	if !j.Lost(raised.Add(60 * time.Hour)) {
		t.Error("after 48 hours is lost")
	}
}

func TestLostOnlyForInTransitJourneys(t *testing.T) {
	for _, st := range []State{StateHandedOver, StateAtStation, StateOutForDelivery} {
		j := inState(t, st)
		_, err := j.RaiseException(j.LastScanAt().Add(30 * time.Hour))
		mustNoErr(t, err)
		if j.Lost(at(5000)) {
			t.Errorf("%s must never be declared lost by silence", st)
		}
	}
}

func TestAScanAfterTheExceptionAnswersIt(t *testing.T) {
	j := inState(t, StateInTransit)
	raised := j.LastScanAt().Add(30 * time.Hour)
	_, err := j.RaiseException(raised)
	mustNoErr(t, err)
	_, err = j.RecordScan(scan("alive", ScanDeparture, 100), raised.Add(time.Hour))
	mustNoErr(t, err)
	if j.Lost(at(5000)) {
		t.Fatal("a scan after the exception means the package is moving")
	}
}
