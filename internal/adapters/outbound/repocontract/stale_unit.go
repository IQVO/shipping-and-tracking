package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// seedAt stores pair n and drives its journey with scans up to the given state;
// the last applied scan is at the given hour.
func seedAt(t *testing.T, f Fixture, n int, steps []journey.ScanType, lastHour int) *journey.Journey {
	t.Helper()
	ctx := context.Background()
	p := Seed(t, f, n)
	for i, typ := range steps {
		h := lastHour - len(steps) + 1 + i
		Scan(t, p.Journey, string(typ), typ, h)
		noErr(t, f.Journeys.Save(ctx, p.Journey))
	}
	return p.Journey
}

func trackingsOf(js []*journey.Journey) []string {
	out := make([]string, len(js))
	for i, j := range js {
		out[i] = string(j.TrackingNumber())
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var inTransitSteps = []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture}

func raise(t *testing.T, f Fixture, j *journey.Journey, h int) {
	t.Helper()
	_, err := j.RaiseException(Hour(h))
	noErr(t, err)
	noErr(t, f.Journeys.Save(context.Background(), j))
}

// seedStale stores the journeys of the stale cases and returns their tracking
// numbers by name.
func seedStale(t *testing.T, f Fixture) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	name := func(k string, j *journey.Journey) { out[k] = string(j.TrackingNumber()) }
	name("labelled", seedAt(t, f, 1, nil, 0))
	name("h10", seedAt(t, f, 2, inTransitSteps, 10))
	name("h5", seedAt(t, f, 3, inTransitSteps, 5))
	name("h50", seedAt(t, f, 4, inTransitSteps, 50))
	handed := seedAt(t, f, 5, inTransitSteps[:1], 8)
	raise(t, f, handed, 30)
	name("handedExcepted", handed)
	transit := seedAt(t, f, 6, inTransitSteps, 7)
	raise(t, f, transit, 30)
	name("transitExcepted", transit)
	voided := Seed(t, f, 7)
	noErr(t, voided.Journey.Void(Hour(2)))
	noErr(t, f.Journeys.Save(ctx, voided.Journey))
	name("voided", voided.Journey)
	return out
}

var staleCases = []named{
	{"lists open journeys past LABELLED before the cutoff, oldest first", func(t *testing.T, f Fixture) {
		n := seedStale(t, f)
		got, err := f.Journeys.ListStale(context.Background(), repository.StaleFilter{Before: Hour(20), Limit: 10})
		noErr(t, err)
		want := []string{n["h5"], n["transitExcepted"], n["h10"]}
		if !equalStrings(trackingsOf(got), want) {
			t.Fatalf("listed %v, want %v", trackingsOf(got), want)
		}
	}},
	{"the cutoff is exclusive", func(t *testing.T, f Fixture) {
		n := seedStale(t, f)
		got, err := f.Journeys.ListStale(context.Background(), repository.StaleFilter{Before: Hour(10), Limit: 10})
		noErr(t, err)
		if want := []string{n["h5"], n["transitExcepted"]}; !equalStrings(trackingsOf(got), want) {
			t.Fatalf("listed %v, want %v", trackingsOf(got), want)
		}
	}},
	{"an excepted journey that is not IN_TRANSIT is not listed until a new scan arrives", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		n := seedStale(t, f)
		got, err := f.Journeys.ListStale(ctx, repository.StaleFilter{Before: Hour(100), Limit: 10})
		noErr(t, err)
		for _, tn := range trackingsOf(got) {
			if tn == n["handedExcepted"] || tn == n["voided"] || tn == n["labelled"] {
				t.Fatalf("%s must not be listed", tn)
			}
		}
		j, err := f.Journeys.Get(ctx, shared.TrackingNumber(n["handedExcepted"]))
		noErr(t, err)
		Scan(t, j, "again", journey.ScanDeparture, 40)
		noErr(t, f.Journeys.Save(ctx, j))
		got, err = f.Journeys.ListStale(ctx, repository.StaleFilter{Before: Hour(100), Limit: 10})
		noErr(t, err)
		if !contains(trackingsOf(got), n["handedExcepted"]) {
			t.Fatalf("a fresh scan answers the exception, so %s is a candidate again: %v", n["handedExcepted"], trackingsOf(got))
		}
	}},
	{"pages by keyset: limit then After", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		n := seedStale(t, f)
		page1, err := f.Journeys.ListStale(ctx, repository.StaleFilter{Before: Hour(20), Limit: 2})
		noErr(t, err)
		if want := []string{n["h5"], n["transitExcepted"]}; !equalStrings(trackingsOf(page1), want) {
			t.Fatalf("page 1 = %v, want %v", trackingsOf(page1), want)
		}
		last := page1[len(page1)-1]
		page2, err := f.Journeys.ListStale(ctx, repository.StaleFilter{
			Before: Hour(20), Limit: 2,
			After: repository.StaleCursor{LastScanAt: last.LastScanAt(), TrackingNumber: string(last.TrackingNumber())},
		})
		noErr(t, err)
		if want := []string{n["h10"]}; !equalStrings(trackingsOf(page2), want) {
			t.Fatalf("page 2 = %v, want %v", trackingsOf(page2), want)
		}
	}},
	{"ties on the last scan instant order by tracking number", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		a := seedAt(t, f, 1, inTransitSteps, 5)
		b := seedAt(t, f, 2, inTransitSteps, 5)
		got, err := f.Journeys.ListStale(ctx, repository.StaleFilter{Before: Hour(20), Limit: 10})
		noErr(t, err)
		if want := []string{string(a.TrackingNumber()), string(b.TrackingNumber())}; !equalStrings(trackingsOf(got), want) {
			t.Fatalf("listed %v, want %v", trackingsOf(got), want)
		}
		after, err := f.Journeys.ListStale(ctx, repository.StaleFilter{
			Before: Hour(20), Limit: 10,
			After: repository.StaleCursor{LastScanAt: a.LastScanAt(), TrackingNumber: string(a.TrackingNumber())},
		})
		noErr(t, err)
		if want := []string{string(b.TrackingNumber())}; !equalStrings(trackingsOf(after), want) {
			t.Fatalf("after a: %v, want %v", trackingsOf(after), want)
		}
	}},
	{"no candidate is an empty list", func(t *testing.T, f Fixture) {
		got, err := f.Journeys.ListStale(context.Background(), repository.StaleFilter{Before: Hour(20), Limit: 10})
		noErr(t, err)
		if len(got) != 0 {
			t.Fatalf("listed %v", trackingsOf(got))
		}
	}},
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

var unitOfWorkCases = []named{
	{"a failing unit stores nothing", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		boom := errors.New("boom")
		p := NewPair(t, 1)
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			noErr(t, f.Labels.Save(ctx, p.Label))
			noErr(t, f.Journeys.Save(ctx, p.Journey))
			return boom
		})
		wantErr(t, err, boom)
		_, err = f.Labels.Get(ctx, p.Label.Ref())
		if err == nil {
			t.Fatal("the label must have rolled back")
		}
		_, err = f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		if err == nil {
			t.Fatal("the journey must have rolled back")
		}
	}},
	{"a committed unit stores aggregates and outbox rows together", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := NewPair(t, 1)
		_, evs, err := label.Issue(label.NewLabel{
			Ref: "lbl-x", TrackingNumber: "RNX", PONumber: "PO-x", PackageNo: 1, AssignmentID: "a", OrderID: "o",
			Tier: shared.TierNextDay, StopKey: p.Label.StopKey(), DestinationPostalPrefix: "01310",
		}, T0)
		noErr(t, err)
		noErr(t, f.UoW.Do(ctx, func(ctx context.Context) error {
			noErr(t, f.Labels.Save(ctx, p.Label))
			return f.Outbox.Append(ctx, evs...)
		}))
		if _, err := f.Labels.Get(ctx, p.Label.Ref()); err != nil {
			t.Fatalf("the committed label must be stored: %v", err)
		}
		assertOutbox(t, f, len(evs))
	}},
	{"a failing unit appends no outbox row", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := NewPair(t, 1)
		boom := errors.New("boom")
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			_, evs, _ := label.Issue(label.NewLabel{
				Ref: "lbl-x", TrackingNumber: "RNX", PONumber: "PO-x", PackageNo: 1, AssignmentID: "a", OrderID: "o",
				Tier: shared.TierNextDay, StopKey: p.Label.StopKey(), DestinationPostalPrefix: "01310",
			}, T0)
			noErr(t, f.Outbox.Append(ctx, evs...))
			return boom
		})
		wantErr(t, err, boom)
		assertOutbox(t, f, 0)
	}},
	{"a nested unit joins the outer one", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		boom := errors.New("boom")
		p := NewPair(t, 1)
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			noErr(t, f.UoW.Do(ctx, func(ctx context.Context) error { return f.Labels.Save(ctx, p.Label) }))
			return boom
		})
		wantErr(t, err, boom)
		if _, err := f.Labels.Get(ctx, p.Label.Ref()); err == nil {
			t.Fatal("the inner work must roll back with the outer unit")
		}
	}},
	{"a panicking unit rolls back and the panic propagates", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := NewPair(t, 1)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("the panic must propagate")
				}
			}()
			_ = f.UoW.Do(ctx, func(ctx context.Context) error {
				noErr(t, f.Labels.Save(ctx, p.Label))
				panic("kaboom")
			})
		}()
		if _, err := f.Labels.Get(ctx, p.Label.Ref()); err == nil {
			t.Fatal("the label must have rolled back")
		}
	}},
}

func assertOutbox(t *testing.T, f Fixture, want int) {
	t.Helper()
	if f.OutboxCount == nil {
		return
	}
	got, err := f.OutboxCount(context.Background())
	noErr(t, err)
	if got != want {
		t.Fatalf("outbox rows = %d, want %d", got, want)
	}
}
