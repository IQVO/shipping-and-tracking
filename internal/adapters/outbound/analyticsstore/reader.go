package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Every query filters the
// outcome instant with `>= from AND < to`, and days are UTC calendar days.
// SQL only counts, sums and takes percentile_cont; the scan sets, the ranks
// and every derived figure (means, rates) come from internal/analytics/report.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// touchesSQL: the delivered packages of the range, their tier (UNKNOWN until
// the label is projected) and their touch count, then per day and tier the
// count, the sum and the two percentiles.
//
//	$1 from, $2 to, $3 outcome DELIVERED, $4 unknown tier, $5 touch scan types,
//	$6 p50 rank, $7 p90 rank
const touchesSQL = `
	WITH per_package AS (
		SELECT (o.outcome_at AT TIME ZONE 'UTC')::date AS day,
		       COALESCE(l.tier, $4) AS tier,
		       (SELECT count(*) FROM journey_scans s
		         WHERE s.tracking_number = o.tracking_number AND s.scan_type = ANY($5::text[])) AS touches
		FROM package_outcomes o
		LEFT JOIN package_labels l ON l.tracking_number = o.tracking_number
		WHERE o.outcome = $3 AND o.outcome_at >= $1 AND o.outcome_at < $2
	)
	SELECT day, tier, count(*), COALESCE(sum(touches), 0)::bigint,
	       percentile_cont($6::float8) WITHIN GROUP (ORDER BY touches::float8),
	       percentile_cont($7::float8) WITHIN GROUP (ORDER BY touches::float8)
	FROM per_package
	GROUP BY day, tier
	ORDER BY day, tier`

// PackageTouches implements report.Reader.
func (r *Reader) PackageTouches(ctx context.Context, rg report.Range) ([]report.TouchesDay, error) {
	return collect(ctx, r.pool, touchesSQL, func(rows pgx.Rows) (report.TouchesDay, error) {
		var d report.TouchesDay
		err := rows.Scan(&d.Day, &d.Tier, &d.PackagesDelivered, &d.Touches, &d.P50, &d.P90)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.OutcomeDelivered, report.UnknownTier(), report.TouchScanTypes(),
		report.TouchesP50(), report.TouchesP90())
}

// outcomesSQL: every outcome of the range with the failed attempts of its
// package, then per day the counts per outcome and the failed attempts of the
// delivered packages.
//
//	$1 from, $2 to, $3 failed-attempt scan types, $4 DELIVERED, $5 RETURNED, $6 LOST
const outcomesSQL = `
	WITH per_package AS (
		SELECT (o.outcome_at AT TIME ZONE 'UTC')::date AS day, o.outcome,
		       (SELECT count(*) FROM journey_scans s
		         WHERE s.tracking_number = o.tracking_number AND s.scan_type = ANY($3::text[])) AS failed
		FROM package_outcomes o
		WHERE o.outcome_at >= $1 AND o.outcome_at < $2
	)
	SELECT day,
	       count(*) FILTER (WHERE outcome = $4),
	       count(*) FILTER (WHERE outcome = $4 AND failed = 0),
	       COALESCE(sum(failed) FILTER (WHERE outcome = $4), 0)::bigint,
	       count(*) FILTER (WHERE outcome = $5),
	       count(*) FILTER (WHERE outcome = $6)
	FROM per_package
	GROUP BY day
	ORDER BY day`

// DeliveryOutcomes implements report.Reader.
func (r *Reader) DeliveryOutcomes(ctx context.Context, rg report.Range) ([]report.OutcomeDay, error) {
	return collect(ctx, r.pool, outcomesSQL, func(rows pgx.Rows) (report.OutcomeDay, error) {
		var d report.OutcomeDay
		err := rows.Scan(&d.Day, &d.Delivered, &d.FirstAttemptDelivered, &d.FailedAttempts, &d.Returned, &d.Lost)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.FailedAttemptScanTypes(), report.OutcomeDelivered, report.OutcomeReturned, report.OutcomeLost)
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

// Ping checks the analytical database answers (the reports' /readyz).
func (r *Reader) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// collect runs query with args and scans every row; the result is a non-nil
// slice even when empty.
func collect[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("analyticsstore: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: rows: %w", err)
	}
	return out, nil
}
