package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// store is what both twins implement: the writer and the reader port.
type store interface {
	report.Projection
	report.Reader
}

// runContract is THE contract of the analytical store (ADR 0012). It runs
// against MemoryStore (memory_test.go) and against real Postgres
// (postgres_integration_test.go); newStore returns an EMPTY store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Run("an empty store answers empty, non-nil reports", func(t *testing.T) { contractEmpty(t, newStore(t)) })
	t.Run("a replayed id is a no-op", func(t *testing.T) { contractReplay(t, newStore(t)) })
	t.Run("package touches per day and tier", func(t *testing.T) { contractTouches(t, newStore(t)) })
	t.Run("delivery outcomes per day", func(t *testing.T) { contractOutcomes(t, newStore(t)) })
	t.Run("the freshness basis is the newest CloudEvents time", func(t *testing.T) { contractLastEvent(t, newStore(t)) })
	t.Run("an invalid fact is rejected and leaves nothing", func(t *testing.T) { contractRejected(t, newStore(t)) })
}

var (
	ctx       = context.Background()
	nov3      = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	nov4      = nov3.Add(24 * time.Hour)
	fullRange = report.Range{From: nov3, To: nov3.Add(48 * time.Hour)}
)

// facts builds the events of one package; every event gets a fresh id.
type facts struct {
	t    *testing.T
	s    store
	tn   string
	seq  *int
	last time.Time // the CloudEvents time of the newest event applied
}

func newFacts(t *testing.T, s store, seq *int, tn string) *facts {
	return &facts{t: t, s: s, tn: tn, seq: seq}
}

func (f *facts) apply(e report.Event) {
	f.t.Helper()
	*f.seq++
	e.EventID = fmt.Sprintf("evt-%s-%d", f.tn, *f.seq)
	e.TrackingNumber = f.tn
	e.At = e.OccurredAt.Add(time.Minute)
	applied, err := f.s.Apply(ctx, e)
	if err != nil || !applied {
		f.t.Fatalf("apply %s %s: applied=%v err=%v", e.Kind, e.EventID, applied, err)
	}
	f.last = e.At
}

func (f *facts) label(tier string, at time.Time) *facts {
	f.apply(report.Event{Kind: report.KindLabelIssued, LabelRef: "lbl-" + f.tn, Tier: tier, OccurredAt: at})
	return f
}

func (f *facts) scans(at time.Time, types ...string) *facts {
	for i, st := range types {
		f.apply(report.Event{Kind: report.KindPackageScanned, ScanID: fmt.Sprintf("%s-scan-%d", f.tn, i), ScanType: st,
			OccurredAt: at.Add(time.Duration(i) * time.Minute)})
	}
	return f
}

func (f *facts) outcome(kind report.Kind, at time.Time) *facts {
	f.apply(report.Event{Kind: kind, OccurredAt: at})
	return f
}

// seedScenario writes the fixture both report contracts read (see the ADR's
// KPI definitions). Range [nov3, nov5).
func seedScenario(t *testing.T, s store) {
	t.Helper()
	seq := 0
	early := nov3.Add(-48 * time.Hour)
	// A NEXT_DAY, first attempt: SHIPMENT_CONFIRMED and DELIVERED are not touches -> 5.
	// Its label is projected AFTER its delivery (label and journey partitions are independent).
	newFacts(t, s, &seq, "A").
		scans(early, "SHIPMENT_CONFIRMED", "PICKUP_DEPARTURE", "DEPARTURE", "TRANSIT_ARRIVAL", "STATION_ARRIVAL", "OUT_FOR_DELIVERY", "DELIVERED").
		outcome(report.KindPackageDelivered, nov3.Add(10*time.Hour)).label("NEXT_DAY", early)
	// B NEXT_DAY, two failed attempts before delivery, the last second of nov3 -> 7 touches.
	newFacts(t, s, &seq, "B").label("NEXT_DAY", early).
		scans(early, "PICKUP_DEPARTURE", "STATION_ARRIVAL", "OUT_FOR_DELIVERY", "DELIVERY_FAILED", "OUT_FOR_DELIVERY",
			"DELIVERY_FAILED", "OUT_FOR_DELIVERY", "DELIVERED").
		outcome(report.KindPackageDelivered, nov4.Add(-time.Second))
	// H NEXT_DAY, first attempt, 2 touches.
	newFacts(t, s, &seq, "H").label("NEXT_DAY", early).scans(early, "PICKUP_DEPARTURE", "OUT_FOR_DELIVERY", "DELIVERED").
		outcome(report.KindPackageDelivered, nov3.Add(12*time.Hour))
	// C has no label -> UNKNOWN; its scans arrive after its delivery fact.
	newFacts(t, s, &seq, "C").outcome(report.KindPackageDelivered, nov4.Add(8*time.Hour)).
		scans(early, "PICKUP_DEPARTURE", "STATION_ARRIVAL", "OUT_FOR_DELIVERY", "DELIVERED")
	// D returned after three failures (counted as a return, never as attempts); E lost.
	newFacts(t, s, &seq, "D").label("SAME_DAY", early).
		scans(early, "OUT_FOR_DELIVERY", "DELIVERY_FAILED", "OUT_FOR_DELIVERY", "DELIVERY_FAILED", "OUT_FOR_DELIVERY", "DELIVERY_FAILED").
		outcome(report.KindPackageReturned, nov4.Add(9*time.Hour))
	newFacts(t, s, &seq, "E").label("STANDARD", early).outcome(report.KindPackageLost, nov4.Add(13*time.Hour))
	// Outside the range: exactly at To (exclusive) and one second before From.
	newFacts(t, s, &seq, "F").label("NEXT_DAY", early).scans(early, "DEPARTURE").
		outcome(report.KindPackageDelivered, fullRange.To)
	newFacts(t, s, &seq, "G").label("NEXT_DAY", early).outcome(report.KindPackageDelivered, nov3.Add(-time.Second))
}

func contractEmpty(t *testing.T, s store) {
	touches, err := s.PackageTouches(ctx, fullRange)
	if err != nil || touches == nil || len(touches) != 0 {
		t.Fatalf("touches = %#v, %v; want empty non-nil", touches, err)
	}
	outcomes, err := s.DeliveryOutcomes(ctx, fullRange)
	if err != nil || outcomes == nil || len(outcomes) != 0 {
		t.Fatalf("outcomes = %#v, %v; want empty non-nil", outcomes, err)
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("last = %v, %v; want nil", last, err)
	}
}

func contractReplay(t *testing.T, s store) {
	e := report.Event{Kind: report.KindPackageDelivered, EventID: "evt-1", TrackingNumber: "A", At: nov3.Add(time.Hour), OccurredAt: nov3}
	if applied, err := s.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.Apply(ctx, e); err != nil || applied {
		t.Fatalf("replay: applied=%v err=%v; want a no-op", applied, err)
	}
	outcomes, err := s.DeliveryOutcomes(ctx, fullRange)
	if err != nil || len(outcomes) != 1 || outcomes[0].Delivered != 1 {
		t.Fatalf("outcomes after a replay = %+v, %v; want one delivery", outcomes, err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func contractTouches(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.PackageTouches(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.TouchesDay{
		{Day: nov3, Tier: "NEXT_DAY", PackagesDelivered: 3, Touches: 14, P50: 5, P90: 6.6},
		{Day: nov4, Tier: report.UnknownTier(), PackagesDelivered: 1, Touches: 3, P50: 3, P90: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("touches = %+v\nwant %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.Day.Equal(w.Day) || g.Tier != w.Tier || g.PackagesDelivered != w.PackagesDelivered || g.Touches != w.Touches ||
			!near(g.P50, w.P50) || !near(g.P90, w.P90) {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
}

func contractOutcomes(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.DeliveryOutcomes(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.OutcomeDay{
		{Day: nov3, Delivered: 3, FirstAttemptDelivered: 2, FailedAttempts: 2},
		{Day: nov4, Delivered: 1, FirstAttemptDelivered: 1, Returned: 1, Lost: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("outcomes = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if !got[i].Day.Equal(want[i].Day) || got[i].Day.Location() != time.UTC {
			t.Errorf("row %d day = %v, want %v UTC", i, got[i].Day, want[i].Day)
		}
		got[i].Day = want[i].Day
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	one, err := s.DeliveryOutcomes(ctx, report.Range{From: nov4, To: fullRange.To})
	if err != nil || len(one) != 1 || !one[0].Day.Equal(nov4) {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

func contractLastEvent(t *testing.T, s store) {
	seq := 0
	f := newFacts(t, s, &seq, "A").label("NEXT_DAY", nov3.Add(5*time.Hour)).outcome(report.KindPackageLost, nov3.Add(2*time.Hour))
	last, err := s.LastEventAt(ctx)
	newest := nov3.Add(5*time.Hour + time.Minute)
	if err != nil || last == nil || !last.Equal(newest) || last.Location() != time.UTC {
		t.Fatalf("last = %v, %v; want %v UTC (the newest, not the last applied %v)", last, err, newest, f.last)
	}
}

func contractRejected(t *testing.T, s store) {
	bad := []report.Event{
		{Kind: report.KindPackageScanned, EventID: "evt-bad-1", At: nov3, OccurredAt: nov3, ScanID: "s", ScanType: "DEPARTURE"},
		{Kind: "LabelVoided", EventID: "evt-bad-2", TrackingNumber: "A", At: nov3, OccurredAt: nov3},
		{Kind: report.KindLabelIssued, EventID: "evt-bad-3", TrackingNumber: "A", At: nov3, OccurredAt: nov3, LabelRef: "l"},
	}
	for _, e := range bad {
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, report.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", e.EventID, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
}
