package report_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

func TestPackageTouchesDerivesTheMean(t *testing.T) {
	day := utcDay(2026, 11, 3)
	got := report.PackageTouches([]report.TouchesDay{
		{Day: day, Tier: "NEXT_DAY", PackagesDelivered: 4, Touches: 18, P50: 4.5, P90: 6},
		{Day: day, Tier: "SAME_DAY", PackagesDelivered: 0, Touches: 0},
	})
	if len(got) != 2 || got[0].Mean != 4.5 || got[0].Tier != "NEXT_DAY" || got[0].P90 != 6 || got[1].Mean != 0 {
		t.Fatalf("rows = %+v", got)
	}
	if empty := report.PackageTouches(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestDeliveryOutcomesDerivesTheKPIs(t *testing.T) {
	day := utcDay(2026, 11, 3)
	got := report.DeliveryOutcomes([]report.OutcomeDay{
		{Day: day, Delivered: 6, FirstAttemptDelivered: 3, FailedAttempts: 4, Returned: 1, Lost: 1},
	})
	mean := 10.0 / 6
	want := []report.OutcomeRow{{
		OutcomeDay: report.OutcomeDay{Day: day, Delivered: 6, FirstAttemptDelivered: 3, FailedAttempts: 4, Returned: 1, Lost: 1},
		Resolved:   8, FirstAttemptSuccessRate: 3.0 / 8, LostRate: 1.0 / 8, ReturnToSenderRate: 1.0 / 8, MeanAttempts: &mean,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}
}

func TestDeliveryOutcomesWithoutDeliveries(t *testing.T) {
	got := report.DeliveryOutcomes([]report.OutcomeDay{{Day: utcDay(2026, 11, 4), Returned: 1, Lost: 3}})
	if got[0].MeanAttempts != nil {
		t.Fatalf("mean attempts must be nil without deliveries, got %v", *got[0].MeanAttempts)
	}
	if got[0].Resolved != 4 || got[0].LostRate != 0.75 || got[0].ReturnToSenderRate != 0.25 || got[0].FirstAttemptSuccessRate != 0 {
		t.Fatalf("row = %+v", got[0])
	}
	if empty := report.DeliveryOutcomes(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestRate(t *testing.T) {
	cases := []struct {
		num, den int
		want     float64
	}{{1, 4, 0.25}, {3, 3, 1}, {0, 5, 0}, {2, 0, 0}, {2, -1, 0}, {1, 1, 1}}
	for _, c := range cases {
		if got := report.Rate(c.num, c.den); got != c.want {
			t.Errorf("Rate(%d, %d) = %v, want %v", c.num, c.den, got, c.want)
		}
	}
}

func TestMeanAttempts(t *testing.T) {
	if m := report.MeanAttempts(0, 3); m != nil {
		t.Fatalf("MeanAttempts(0, 3) = %v, want nil", *m)
	}
	if m := report.MeanAttempts(-1, 0); m != nil {
		t.Fatalf("MeanAttempts(-1, 0) = %v, want nil", *m)
	}
	if m := report.MeanAttempts(1, 0); m == nil || *m != 1 {
		t.Fatalf("MeanAttempts(1, 0) = %v, want 1", m)
	}
	if m := report.MeanAttempts(4, 2); m == nil || *m != 1.5 {
		t.Fatalf("MeanAttempts(4, 2) = %v, want 1.5", m)
	}
}

func TestPercentileMatchesPercentileCont(t *testing.T) {
	values := []float64{7, 1, 3, 5}
	cases := []struct{ p, want float64 }{{0.5, 4}, {0.9, 6.4}, {0, 1}, {1, 7}, {0.25, 2.5}}
	for _, c := range cases {
		if got := report.Percentile(values, c.p); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("Percentile(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if !reflect.DeepEqual(values, []float64{7, 1, 3, 5}) {
		t.Fatalf("Percentile modified its input: %v", values)
	}
	if got := report.Percentile(nil, 0.5); got != 0 {
		t.Fatalf("empty = %v, want 0", got)
	}
	if got := report.Percentile([]float64{3}, 0.9); got != 3 {
		t.Fatalf("single = %v, want 3", got)
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := report.ComputeFreshness(nil, now); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied must give nil/nil, got %+v", f)
	}
	asOf := time.Date(2026, 11, 8, 18, 29, 0, 0, time.UTC) // now is 18:30 UTC
	f := report.ComputeFreshness(&asOf, now)
	if f.AsOf == nil || !f.AsOf.Equal(asOf) || f.AsOf.Location() != time.UTC || f.LagSeconds == nil || *f.LagSeconds != 60 {
		t.Fatalf("freshness = %+v", f)
	}
	future := now.Add(time.Minute)
	if f := report.ComputeFreshness(&future, now); *f.LagSeconds != 0 {
		t.Fatalf("a clock behind the event must read zero lag, got %v", *f.LagSeconds)
	}
}
