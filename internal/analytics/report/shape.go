package report

import (
	"math"
	"sort"
	"time"
)

// TouchesRow is a TouchesDay plus its mean touches per delivered package.
type TouchesRow struct {
	TouchesDay
	Mean float64
}

// PackageTouches derives each row's mean. Order is preserved; a nil input
// yields an empty, non-nil slice.
func PackageTouches(days []TouchesDay) []TouchesRow {
	out := make([]TouchesRow, 0, len(days))
	for _, d := range days {
		out = append(out, TouchesRow{TouchesDay: d, Mean: Rate(d.Touches, d.PackagesDelivered)})
	}
	return out
}

// OutcomeRow is an OutcomeDay plus the KPIs of ADR 0012: Resolved = delivered +
// returned + lost; the first-attempt success, lost and return-to-sender rates
// over Resolved; and the mean attempts per delivered package (nil on a day
// without deliveries).
type OutcomeRow struct {
	OutcomeDay
	Resolved                int
	FirstAttemptSuccessRate float64
	LostRate                float64
	ReturnToSenderRate      float64
	MeanAttempts            *float64
}

// DeliveryOutcomes derives each day's KPIs. Order is preserved; a nil input
// yields an empty, non-nil slice.
func DeliveryOutcomes(days []OutcomeDay) []OutcomeRow {
	out := make([]OutcomeRow, 0, len(days))
	for _, d := range days {
		resolved := d.Delivered + d.Returned + d.Lost
		out = append(out, OutcomeRow{
			OutcomeDay:              d,
			Resolved:                resolved,
			FirstAttemptSuccessRate: Rate(d.FirstAttemptDelivered, resolved),
			LostRate:                Rate(d.Lost, resolved),
			ReturnToSenderRate:      Rate(d.Returned, resolved),
			MeanAttempts:            MeanAttempts(d.Delivered, d.FailedAttempts),
		})
	}
	return out
}

// Rate is num/den, or 0 when den is not positive (nothing to rate).
func Rate(num, den int) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// MeanAttempts is (delivered + failed) / delivered: every delivered package
// took its failed attempts plus the successful one. It is nil when nothing was
// delivered.
func MeanAttempts(delivered, failed int) *float64 {
	if delivered <= 0 {
		return nil
	}
	m := Rate(delivered+failed, delivered)
	return &m
}

// Percentile is the continuous percentile (linear interpolation between
// closest ranks, p in [0,1]) of values, the same definition as Postgres'
// percentile_cont. values need not be sorted; it is not modified. An empty
// input yields 0.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := p * float64(len(sorted)-1)
	lo := math.Floor(rank)
	hi := math.Ceil(rank)
	return sorted[int(lo)] + (sorted[int(hi)]-sorted[int(lo)])*(rank-lo)
}

// Freshness is the projection's freshness: AsOf is the CloudEvents time of the
// newest event applied and LagSeconds is now - AsOf. Both are nil while the
// projection has applied nothing.
type Freshness struct {
	AsOf       *time.Time
	LagSeconds *float64
}

// ComputeFreshness derives the lag from the newest applied event time. A clock
// that is behind the event (skew) reads as zero lag, never negative.
func ComputeFreshness(asOf *time.Time, now time.Time) Freshness {
	if asOf == nil {
		return Freshness{}
	}
	// math.Max, not `if lag < 0`: both yield 0 at the boundary, which would
	// leave an equivalent `<` -> `<=` mutant.
	lag := math.Max(0, now.Sub(*asOf).Seconds())
	utc := asOf.UTC()
	return Freshness{AsOf: &utc, LagSeconds: &lag}
}
