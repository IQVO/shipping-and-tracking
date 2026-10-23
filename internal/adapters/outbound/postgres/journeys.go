package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func tierOf(s string) shared.Tier { return shared.Tier(s) }

// JourneyRepo is the pgx-backed ports.JourneyRepository.
type JourneyRepo struct{ pool *pgxpool.Pool }

// NewJourneyRepo constructs a JourneyRepo over pool.
func NewJourneyRepo(pool *pgxpool.Pool) *JourneyRepo { return &JourneyRepo{pool: pool} }

var _ ports.JourneyRepository = (*JourneyRepo)(nil)

const journeyColumns = `tracking_number, label_ref, po_number, assignment_id, order_id, tier, state, started_at, last_scan_at,
	last_node_id, handed_over_at, delivered_at, closed_at, last_exception_at, version`

func scanJourneyRow(row pgx.Row) (journey.Snapshot, error) {
	var (
		s                                             journey.Snapshot
		tier, state                                   string
		lastScan, handed, delivered, closed, excepted *time.Time
		node                                          *string
	)
	if err := row.Scan(&s.TrackingNumber, &s.LabelRef, &s.PONumber, &s.AssignmentID, &s.OrderID, &tier, &state, &s.StartedAt,
		&lastScan, &node, &handed, &delivered, &closed, &excepted, &s.Version); err != nil {
		return journey.Snapshot{}, err
	}
	s.Tier, s.State, s.LastNodeID = tierOf(tier), journey.State(state), deref(node)
	s.StartedAt = s.StartedAt.UTC()
	s.LastScanAt, s.HandedOverAt, s.DeliveredAt = orZero(lastScan), orZero(handed), orZero(delivered)
	s.ClosedAt, s.LastExceptionAt = orZero(closed), orZero(excepted)
	return s, nil
}

// Get implements ports.JourneyRepository.
func (r *JourneyRepo) Get(ctx context.Context, tn shared.TrackingNumber) (*journey.Journey, error) {
	q := queryFor(ctx, r.pool)
	s, err := scanJourneyRow(q.QueryRow(ctx, `SELECT `+journeyColumns+` FROM package_journeys WHERE tracking_number = $1`, string(tn)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: journey %s", ports.ErrNotFound, tn)
	}
	if err != nil {
		return nil, err
	}
	if s.Scans, err = loadScans(ctx, q, string(tn)); err != nil {
		return nil, err
	}
	return journey.Restore(s)
}

func loadScans(ctx context.Context, q querier, tn string) ([]journey.Scan, error) {
	rows, err := q.Query(ctx, `SELECT scan_id, node_id, scan_type, at, applied FROM journey_scans WHERE tracking_number = $1 ORDER BY seq`, tn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []journey.Scan
	for rows.Next() {
		var sc journey.Scan
		var typ string
		if err := rows.Scan(&sc.ID, &sc.NodeID, &typ, &sc.At, &sc.Applied); err != nil {
			return nil, err
		}
		sc.Type, sc.At = journey.ScanType(typ), sc.At.UTC()
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Save implements ports.JourneyRepository.
func (r *JourneyRepo) Save(ctx context.Context, j *journey.Journey) error {
	s := j.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		if s.Version == 1 {
			if err := insertJourney(ctx, q, s); err != nil {
				return err
			}
		} else if err := updateJourney(ctx, q, s); err != nil {
			return err
		}
		return appendScans(ctx, q, s)
	})
}

func insertJourney(ctx context.Context, q querier, s journey.Snapshot) error {
	_, err := q.Exec(ctx, `INSERT INTO package_journeys (`+journeyColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		string(s.TrackingNumber), s.LabelRef, s.PONumber, s.AssignmentID, s.OrderID, string(s.Tier), string(s.State), s.StartedAt.UTC(),
		nullTime(s.LastScanAt), nullString(s.LastNodeID), nullTime(s.HandedOverAt), nullTime(s.DeliveredAt), nullTime(s.ClosedAt),
		nullTime(s.LastExceptionAt), s.Version)
	if _, ok := unique(err); ok {
		return fmt.Errorf("%w: journey %s", ports.ErrConcurrentUpdate, s.TrackingNumber)
	}
	return err
}

func updateJourney(ctx context.Context, q querier, s journey.Snapshot) error {
	tag, err := q.Exec(ctx, `UPDATE package_journeys SET state = $2, last_scan_at = $3, last_node_id = $4, handed_over_at = $5,
		delivered_at = $6, closed_at = $7, last_exception_at = $8, version = $9
		WHERE tracking_number = $1 AND version = $10`,
		string(s.TrackingNumber), string(s.State), nullTime(s.LastScanAt), nullString(s.LastNodeID), nullTime(s.HandedOverAt),
		nullTime(s.DeliveredAt), nullTime(s.ClosedAt), nullTime(s.LastExceptionAt), s.Version, s.Version-1)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: journey %s", ports.ErrConcurrentUpdate, s.TrackingNumber)
	}
	return nil
}

// appendScans inserts the scans the stored journey does not have yet. The
// journey row is locked by the INSERT or UPDATE above, so the count is stable.
func appendScans(ctx context.Context, q querier, s journey.Snapshot) error {
	var stored int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM journey_scans WHERE tracking_number = $1`, string(s.TrackingNumber)).Scan(&stored); err != nil {
		return err
	}
	for i := stored; i < len(s.Scans); i++ {
		sc := s.Scans[i]
		if _, err := q.Exec(ctx, `INSERT INTO journey_scans (tracking_number, seq, scan_id, node_id, scan_type, at, applied)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, string(s.TrackingNumber), i+1, sc.ID, sc.NodeID, string(sc.Type), sc.At.UTC(), sc.Applied); err != nil {
			return fmt.Errorf("append scan %s: %w", sc.ID, err)
		}
	}
	return nil
}

// ListStale implements ports.JourneyRepository.
func (r *JourneyRepo) ListStale(ctx context.Context, f repository.StaleFilter) ([]*journey.Journey, error) {
	q := queryFor(ctx, r.pool)
	var afterAt any
	if !f.After.LastScanAt.IsZero() {
		afterAt = f.After.LastScanAt.UTC()
	}
	rows, err := q.Query(ctx, `
		SELECT tracking_number FROM package_journeys
		WHERE state IN ('HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'OUT_FOR_DELIVERY')
		  AND last_scan_at < $1
		  AND NOT (last_exception_at IS NOT NULL AND last_exception_at >= last_scan_at AND state <> 'IN_TRANSIT')
		  AND ($2::timestamptz IS NULL OR (last_scan_at, tracking_number) > ($2::timestamptz, $3::text))
		ORDER BY last_scan_at, tracking_number
		LIMIT $4`, f.Before.UTC(), afterAt, f.After.TrackingNumber, limitOrDefault(f.Limit))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*journey.Journey, 0, len(ids))
	for _, id := range ids {
		j, err := r.Get(ctx, shared.TrackingNumber(id))
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func limitOrDefault(n int) int {
	if n <= 0 {
		return 200
	}
	return n
}
