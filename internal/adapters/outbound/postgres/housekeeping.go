package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Housekeeping deletes the rows of the technical tables that have outlived
// their purpose (ADR 0005): the replay cache of the Idempotency-Key middleware,
// the relayed outbox rows and the inbox claims of consumed events. It never
// touches an aggregate or a ship-to snapshot (the purge of the latter is a use
// case, ShipTo retention).
type Housekeeping struct{ pool *pgxpool.Pool }

// NewHousekeeping builds the housekeeping over pool.
func NewHousekeeping(pool *pgxpool.Pool) *Housekeeping { return &Housekeeping{pool: pool} }

// PurgeIdempotencyKeys deletes the keys created before the cutoff.
func (h *Housekeeping) PurgeIdempotencyKeys(ctx context.Context, before time.Time) (int64, error) {
	return h.delete(ctx, "idempotency_keys", `DELETE FROM idempotency_keys WHERE created_at < $1`, before)
}

// PurgeRelayedOutbox deletes the outbox rows that were published before the
// cutoff. An unpublished row is never deleted.
func (h *Housekeeping) PurgeRelayedOutbox(ctx context.Context, before time.Time) (int64, error) {
	return h.delete(ctx, "outbox_events", `DELETE FROM outbox_events WHERE published_at IS NOT NULL AND published_at < $1`, before)
}

// PurgeProcessedEvents deletes the inbox claims made before the cutoff.
func (h *Housekeeping) PurgeProcessedEvents(ctx context.Context, before time.Time) (int64, error) {
	return h.delete(ctx, "processed_events", `DELETE FROM processed_events WHERE processed_at < $1`, before)
}

func (h *Housekeeping) delete(ctx context.Context, table, sql string, before time.Time) (int64, error) {
	tag, err := h.pool.Exec(ctx, sql, before)
	if err != nil {
		return 0, fmt.Errorf("purge %s: %w", table, err)
	}
	return tag.RowsAffected(), nil
}
