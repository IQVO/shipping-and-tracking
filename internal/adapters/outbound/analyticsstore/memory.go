package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// MemoryStore is the in-memory twin of Projection and Reader, for unit tests
// of the consumer and the reports. contract_test.go runs the SAME suite
// against it and against Postgres, so its behaviour cannot drift from the real
// store. The shipped binaries never construct it.
type MemoryStore struct {
	mu        sync.Mutex
	processed map[string]time.Time
	labels    map[string]memLabel
	scans     []memScan
	outcomes  map[string]memOutcome
	// Fail, when set, is returned by the next Apply (and cleared), to drive
	// the consumer's transient and rejection paths.
	Fail error
}

type memLabel struct{ tier string }

type memScan struct {
	tracking, scanType string
}

type memOutcome struct {
	outcome string
	at      time.Time
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{processed: map[string]time.Time{}, labels: map[string]memLabel{}, outcomes: map[string]memOutcome{}}
}

var (
	_ report.Projection = (*MemoryStore)(nil)
	_ report.Reader     = (*MemoryStore)(nil)
)

// Apply implements report.Projection with the Postgres store's semantics: an
// event the schema's CHECKs would refuse is rejected before anything is
// recorded, a replay is a no-op.
func (m *MemoryStore) Apply(_ context.Context, e report.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		err := m.Fail
		m.Fail = nil
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	if _, seen := m.processed[e.EventID]; seen {
		return false, nil
	}
	m.processed[e.EventID] = e.At.UTC()
	switch e.Kind {
	case report.KindLabelIssued:
		m.labels[e.TrackingNumber] = memLabel{tier: e.Tier}
	case report.KindPackageScanned:
		m.scans = append(m.scans, memScan{tracking: e.TrackingNumber, scanType: e.ScanType})
	default:
		outcome, _ := report.OutcomeOf(e.Kind) // Validate admitted only the outcome kinds here
		m.outcomes[e.TrackingNumber] = memOutcome{outcome: outcome, at: e.OccurredAt.UTC()}
	}
	return true, nil
}

// countScans is the number of tracking's scans whose type satisfies keep.
func (m *MemoryStore) countScans(tracking string, keep func(string) bool) int {
	n := 0
	for _, s := range m.scans {
		if s.tracking == tracking && keep(s.scanType) {
			n++
		}
	}
	return n
}

// inRange is the outcomes of the range, sorted by tracking number for a
// deterministic aggregation order.
func (m *MemoryStore) inRange(r report.Range) []string {
	var out []string
	for tn, o := range m.outcomes {
		if o.at.Compare(r.From) >= 0 && o.at.Before(r.To) {
			out = append(out, tn)
		}
	}
	sort.Strings(out)
	return out
}

func utcDay(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

// PackageTouches implements report.Reader.
func (m *MemoryStore) PackageTouches(_ context.Context, r report.Range) ([]report.TouchesDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct {
		day  time.Time
		tier string
	}
	groups := map[key][]float64{}
	for _, tn := range m.inRange(r) {
		o := m.outcomes[tn]
		if o.outcome != report.OutcomeDelivered {
			continue
		}
		tier := report.UnknownTier()
		if l, ok := m.labels[tn]; ok {
			tier = l.tier
		}
		k := key{utcDay(o.at), tier}
		groups[k] = append(groups[k], float64(m.countScans(tn, report.IsTouch)))
	}
	out := []report.TouchesDay{}
	for k, touches := range groups {
		sum := 0
		for _, n := range touches {
			sum += int(n)
		}
		out = append(out, report.TouchesDay{Day: k.day, Tier: k.tier, PackagesDelivered: len(touches), Touches: sum,
			P50: report.Percentile(touches, report.TouchesP50()), P90: report.Percentile(touches, report.TouchesP90())})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		return out[i].Tier < out[j].Tier
	})
	return out, nil
}

// DeliveryOutcomes implements report.Reader.
func (m *MemoryStore) DeliveryOutcomes(_ context.Context, r report.Range) ([]report.OutcomeDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	days := map[time.Time]*report.OutcomeDay{}
	for _, tn := range m.inRange(r) {
		o := m.outcomes[tn]
		d := days[utcDay(o.at)]
		if d == nil {
			d = &report.OutcomeDay{Day: utcDay(o.at)}
			days[d.Day] = d
		}
		m.count(d, tn, o.outcome)
	}
	out := make([]report.OutcomeDay, 0, len(days))
	for _, d := range days {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// count adds one package's outcome to its day.
func (m *MemoryStore) count(d *report.OutcomeDay, tracking, outcome string) {
	switch outcome {
	case report.OutcomeDelivered:
		failed := m.countScans(tracking, report.IsFailedAttempt)
		d.Delivered++
		d.FailedAttempts += failed
		if failed == 0 {
			d.FirstAttemptDelivered++
		}
	case report.OutcomeReturned:
		d.Returned++
	default:
		d.Lost++
	}
}

// LastEventAt implements report.Reader.
func (m *MemoryStore) LastEventAt(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	for _, at := range m.processed {
		if last == nil || at.After(*last) {
			a := at
			last = &a
		}
	}
	return last, nil
}

// Ping is the twin's readiness check: always ready.
func (m *MemoryStore) Ping(context.Context) error { return nil }
