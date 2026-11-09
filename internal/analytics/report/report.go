// Package report is the self-contained read-model region of the analytics read
// side (ADR 0012): the facts the projector folds in, the shapes of the
// delivery reports, the KPI rules (which scans are touches and attempts, the
// percentile ranks, means and rates), the date-range rules, the freshness lag,
// and the writer/reader ports the analytical store implements. It imports no
// other internal package (arch test): the scan vocabulary below is
// hand-mirrored from apis/asyncapi.yaml, never imported from the domain, so the
// OLTP model can never leak into the projection.
package report

import (
	"context"
	"errors"
	"time"
)

// Kind is which of the five projected events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The five projected events of retailnetwork.shipping-and-tracking.events.
const (
	KindLabelIssued      Kind = "LabelIssued"
	KindPackageScanned   Kind = "PackageScanned"
	KindPackageDelivered Kind = "PackageDelivered"
	KindPackageReturned  Kind = "PackageReturned"
	KindPackageLost      Kind = "PackageLost"
)

// Outcomes are the terminal states package_outcomes records.
const (
	OutcomeDelivered = "DELIVERED"
	OutcomeReturned  = "RETURNED"
	OutcomeLost      = "LOST"
)

// Event is one decoded analytics fact ready to project. It is the union of the
// five payloads; only the fields of its Kind are set.
type Event struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the use case raised it (freshness)

	// TrackingNumber is the package every fact is about (the subject of the
	// journey events, a payload field of LabelIssued).
	TrackingNumber string
	// OccurredAt is the business instant of the fact: the label's CloudEvents
	// time, the scan's `at`, `deliveredAt`, `returnedAt` or `declaredAt`. The
	// reports bucket days on it.
	OccurredAt time.Time

	// LabelIssued.
	LabelRef string
	Tier     string

	// PackageScanned.
	ScanID   string
	ScanType string
}

// Projection is the WRITER port: Apply records the event id and projects the
// fact in ONE transaction. applied is false when the id was already recorded
// (a replay): nothing changed. An error wrapping ErrRejected is deterministic
// (the store can never accept this event: retry is pointless); any other error
// is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port. Every method answers for the half-open instant
// range [r.From, r.To) of the business instants, bucketed by UTC day. Results
// are ordered by day (then tier) and never nil.
type Reader interface {
	PackageTouches(ctx context.Context, r Range) ([]TouchesDay, error)
	DeliveryOutcomes(ctx context.Context, r Range) ([]OutcomeDay, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// applied (nil while it applied none): the basis of the freshness lag.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// TouchesDay is what the store aggregates for one UTC day of deliveredAt and
// one tier: the delivered packages, the sum of their touches and the touch
// percentiles at TouchesP50 and TouchesP90.
type TouchesDay struct {
	Day               time.Time
	Tier              string
	PackagesDelivered int
	Touches           int
	P50               float64
	P90               float64
}

// OutcomeDay is what the store counts for one UTC day of the outcome instant:
// the packages delivered, delivered at the first attempt, returned to the
// sender and lost, and the failed attempts (DELIVERY_FAILED scans) of the
// delivered packages.
type OutcomeDay struct {
	Day                   time.Time
	Delivered             int
	FirstAttemptDelivered int
	FailedAttempts        int
	Returned              int
	Lost                  int
}
