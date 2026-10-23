package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestSweepRaisesOneNoScanExceptionPerSilence(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...) // NEXT_DAY window 24h, last scan at hour 2
	sweep := usecases.NewSweepSilence(r.deps)

	r.clock.Set(hour(2 + 23))
	res, err := sweep.Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{}) {
		t.Fatalf("inside the window nothing happens, got %+v", res)
	}
	r.clock.Set(hour(2 + 24))
	res, err = sweep.Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{Raised: 1}) {
		t.Fatalf("at the window one exception, got %+v", res)
	}
	ev := r.store.Outbox().Events()
	if last := ev[len(ev)-1]; last.EventName() != "PackageExceptionRaised" {
		t.Fatalf("last event = %s", last.EventName())
	}
	r.clock.Set(hour(2 + 30))
	res, err = sweep.Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{}) {
		t.Fatalf("no second exception for the same silence, got %+v", res)
	}
}

func TestSweepDeclaresLostAfterTheEscalationWindow(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...)
	sweep := usecases.NewSweepSilence(r.deps)
	r.clock.Set(hour(2 + 24))
	_, err := sweep.Execute(context.Background())
	noErr(t, err)
	r.clock.Set(hour(2 + 24 + 47))
	res, err := sweep.Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{}) {
		t.Fatalf("47 hours after the exception: %+v", res)
	}
	r.clock.Set(hour(2 + 24 + 48))
	res, err = sweep.Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{Lost: 1}) {
		t.Fatalf("48 hours after the exception the package is lost, got %+v", res)
	}
	j := r.journey(t, tn)
	if j.State() != journey.StateLost || !j.ClosedAt().Equal(hour(2+24+48)) {
		t.Fatalf("journey = %s closedAt %v", j.State(), j.ClosedAt())
	}
	names := eventNames(r.store.Outbox().Events())
	if names[len(names)-len("PackageLost"):] != "PackageLost" {
		t.Fatalf("events = %s", names)
	}
}

func TestSweepIgnoresJourneysThatAreNotWatchedOrNotSilent(t *testing.T) {
	r := newRig(t)
	r.issued(t) // LABELLED
	sweep := usecases.NewSweepSilence(r.deps)
	r.clock.Set(hour(1000))
	res, err := sweep.Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{}) {
		t.Fatalf("a LABELLED journey is never silent, got %+v", res)
	}
}

func TestSweepPagesThroughEveryCandidate(t *testing.T) {
	r := newRig(t)
	for i := 1; i <= 5; i++ {
		v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-"+string(rune('0'+i)), 1))
		noErr(t, err)
		r.scanTo(t, shared.TrackingNumber(v.TrackingNumber), toInTransit...)
	}
	sweep := usecases.NewSweepSilence(r.deps)
	sweep.PageSize = 2
	r.clock.Set(hour(100))
	res, err := sweep.Execute(context.Background())
	noErr(t, err)
	if res.Raised != 5 {
		t.Fatalf("raised %d of 5 across pages of 2", res.Raised)
	}
}

func TestSweepCountsAConflictAndMovesOn(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...)
	flaky := &flakyJourneys{JourneyRepository: r.store.Journeys(), failures: 3}
	r.deps.Journeys = flaky
	r.clock.Set(hour(100))
	res, err := usecases.NewSweepSilence(r.deps).Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{Conflicts: 1}) {
		t.Fatalf("result = %+v", res)
	}
	flaky.failures = 0
	res, err = usecases.NewSweepSilence(r.deps).Execute(context.Background())
	noErr(t, err)
	if res.Raised != 1 {
		t.Fatalf("the next tick sees it again, got %+v", res)
	}
}

type vanishingJourneys struct{ ports.JourneyRepository }

func (v vanishingJourneys) Get(context.Context, shared.TrackingNumber) (*journey.Journey, error) {
	return nil, ports.ErrNotFound
}

func TestSweepSkipsAJourneyPurgedBetweenThePageAndTheReload(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...)
	r.deps.Journeys = vanishingJourneys{r.store.Journeys()}
	r.clock.Set(hour(100))
	res, err := usecases.NewSweepSilence(r.deps).Execute(context.Background())
	noErr(t, err)
	if res != (usecases.SweepResult{}) {
		t.Fatalf("result = %+v", res)
	}
}

var errBoom = errors.New("boom")

type brokenListRepo struct{ ports.JourneyRepository }

func (brokenListRepo) ListStale(context.Context, repository.StaleFilter) ([]*journey.Journey, error) {
	return nil, errBoom
}

func TestSweepReportsAListFailure(t *testing.T) {
	r := newRig(t)
	r.deps.Journeys = brokenListRepo{r.store.Journeys()}
	_, err := usecases.NewSweepSilence(r.deps).Execute(context.Background())
	wantErr(t, err, errBoom)
}

func TestPurgeShipToUsesThirtyDaysAfterTheJourneyClosed(t *testing.T) {
	r := newRig(t)
	r.issued(t)
	r.clock.Set(hour(1))
	_, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "x") // closes at hour 1
	noErr(t, err)
	purge := usecases.NewPurgeShipTo(r.deps)

	r.clock.Set(hour(1).Add(usecases.ShipToRetention))
	n, err := purge.Execute(context.Background())
	noErr(t, err)
	if n != 0 {
		t.Fatalf("exactly 30 days after closing the snapshot is still kept, purged %d", n)
	}
	r.clock.Set(hour(1).Add(usecases.ShipToRetention + time.Second))
	n, err = purge.Execute(context.Background())
	noErr(t, err)
	if n != 1 || r.store.Counts().Snapshots != 0 {
		t.Fatalf("purged %d, snapshots left %d", n, r.store.Counts().Snapshots)
	}
	if usecases.ShipToRetention != 30*24*time.Hour {
		t.Fatal("retention is 30 days")
	}
}
