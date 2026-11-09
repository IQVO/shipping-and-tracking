package analyticsstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// Projection is the Postgres WRITER (report.Projection). Apply claims the
// event id and projects the fact in ONE transaction: the id is recorded if and
// only if its effect is, so a failure anywhere leaves nothing behind and the
// redelivered event is applied afresh, while a replay of an applied id is a
// no-op.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs a Projection over the writer pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

const claimSQL = `
	INSERT INTO analytics_processed_events (event_id, event_type, occurred_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (event_id) DO NOTHING`

const (
	upsertLabelSQL = `
	INSERT INTO package_labels (tracking_number, label_ref, tier, labelled_at, event_id)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (tracking_number) DO UPDATE
	SET label_ref = EXCLUDED.label_ref, tier = EXCLUDED.tier, labelled_at = EXCLUDED.labelled_at, event_id = EXCLUDED.event_id`

	insertScanSQL = `
	INSERT INTO journey_scans (event_id, tracking_number, scan_id, scan_type, scanned_at)
	VALUES ($1, $2, $3, $4, $5)`

	upsertOutcomeSQL = `
	INSERT INTO package_outcomes (tracking_number, outcome, outcome_at, event_id)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (tracking_number) DO UPDATE
	SET outcome = EXCLUDED.outcome, outcome_at = EXCLUDED.outcome_at, event_id = EXCLUDED.event_id`
)

// Apply implements report.Projection.
func (p *Projection) Apply(ctx context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("analyticsstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	tag, err := tx.Exec(ctx, claimSQL, e.EventID, string(e.Kind), e.At)
	if err != nil {
		return false, classify(fmt.Errorf("analyticsstore: claim event %s: %w", e.EventID, err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied: the deferred rollback ends the empty tx
	}
	if err := project(ctx, tx, e); err != nil {
		return false, classify(fmt.Errorf("analyticsstore: project %s %s: %w", e.Kind, e.EventID, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analyticsstore: commit: %w", err)
	}
	return true, nil
}

// errUnknownKind is deterministic: no retry can make the store accept it.
var errUnknownKind = errors.New("unknown event kind")

func project(ctx context.Context, tx pgx.Tx, e report.Event) error {
	if e.Kind == report.KindLabelIssued {
		_, err := tx.Exec(ctx, upsertLabelSQL, e.TrackingNumber, e.LabelRef, e.Tier, e.OccurredAt, e.EventID)
		return err
	}
	if e.Kind == report.KindPackageScanned {
		_, err := tx.Exec(ctx, insertScanSQL, e.EventID, e.TrackingNumber, e.ScanID, e.ScanType, e.OccurredAt)
		return err
	}
	if outcome, ok := report.OutcomeOf(e.Kind); ok {
		_, err := tx.Exec(ctx, upsertOutcomeSQL, e.TrackingNumber, outcome, e.OccurredAt, e.EventID)
		return err
	}
	return fmt.Errorf("%w %q", errUnknownKind, e.Kind)
}

// classify wraps a Postgres data-exception (SQLSTATE class 22) or
// integrity-violation (class 23) error, and an unknown event kind, in
// report.ErrRejected: the same event can never succeed, so the consumer
// dead-letters it instead of retrying forever. Everything else (connection
// loss, timeouts, locks, failovers, a raised exception) stays transient.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, errUnknownKind) || (errors.As(err, &pgErr) && deterministicClass(pgErr.Code)) {
		return fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	return err
}

// deterministicClass reports whether an SQLSTATE is in class 22 or 23.
func deterministicClass(code string) bool {
	return len(code) == 5 && (code[:2] == "22" || code[:2] == "23")
}
