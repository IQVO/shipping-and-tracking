//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

func TestHousekeepingDeletesOnlyWhatHasOutlivedItsPurpose(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	old, fresh := time.Now().Add(-48*time.Hour), time.Now()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO idempotency_keys (route, key, request_hash, created_at) VALUES ('r', 'old', 'h', $1), ('r', 'new', 'h', $2)`, []any{old, fresh}},
		{`INSERT INTO processed_events (consumer, event_id, processed_at) VALUES ('c', 'old', $1), ('c', 'new', $2)`, []any{old, fresh}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	oldPublished := pgtest.InsertOutboxRow(t, pool, "t", "k1")
	pending := pgtest.InsertOutboxRow(t, pool, "t", "k2")
	newPublished := pgtest.InsertOutboxRow(t, pool, "t", "k3")
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET published_at = $1 WHERE id = $2`, old, oldPublished); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET published_at = $1 WHERE id = $2`, fresh, newPublished); err != nil {
		t.Fatal(err)
	}

	h := postgres.NewHousekeeping(pool)
	cutoff := time.Now().Add(-24 * time.Hour)
	for name, purge := range map[string]func(context.Context, time.Time) (int64, error){
		"idempotency": h.PurgeIdempotencyKeys, "outbox": h.PurgeRelayedOutbox, "inbox": h.PurgeProcessedEvents,
	} {
		if n, err := purge(ctx, cutoff); err != nil || n != 1 {
			t.Errorf("%s purged %d rows (%v), want 1", name, n, err)
		}
	}
	var keys, claims, rows int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM idempotency_keys), (SELECT count(*) FROM processed_events), (SELECT count(*) FROM outbox_events)`).Scan(&keys, &claims, &rows)
	if keys != 1 || claims != 1 || rows != 2 {
		t.Fatalf("left %d keys, %d claims, %d outbox rows; want 1, 1, 2 (the pending row %d survives)", keys, claims, rows, pending)
	}
}
