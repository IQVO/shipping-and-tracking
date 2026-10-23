//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/repocontract"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// The same behavioural contract the in-memory doubles pass, run against a real
// Postgres: every contract case gets its own database.
func TestPostgresPassesTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture {
		pool := pgtest.NewPool(t)
		return repocontract.Fixture{
			Labels: postgres.NewLabelRepo(pool), Snapshots: postgres.NewSnapshotRepo(pool), Journeys: postgres.NewJourneyRepo(pool),
			Catalog: postgres.NewCatalogRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
			Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
			OutboxCount: func(ctx context.Context) (int, error) {
				var n int
				err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n)
				return n, err
			},
		}
	})
}

// TestMigrationsGoUpDownUp proves the embedded migrations are reversible and
// that running them twice is a no-op.
func TestMigrationsGoUpDownUp(t *testing.T) {
	url := pgtest.EmptyURL(t)
	for step, run := range []func(string) error{
		postgres.RunMigrations, postgres.RunMigrations, postgres.RollbackMigrations, postgres.RunMigrations,
	} {
		if err := run(url); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	want := []string{"catalog_dimensions", "idempotency_keys", "journey_scans", "outbox_events", "package_journeys",
		"processed_events", "ship_to_snapshots", "shipping_labels"}
	rows, err := pool.Query(context.Background(),
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'schema_migrations' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	if err := postgres.RollbackMigrations(url); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after the down migration %d tables remain (%v)", left, err)
	}
}

// TestMissesAreThePortSentinel pins that no Postgres miss leaks pgx.ErrNoRows.
func TestMissesAreThePortSentinel(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	_, e1 := postgres.NewLabelRepo(pool).Get(ctx, "nope")
	_, e2 := postgres.NewLabelRepo(pool).FindByPackage(ctx, "PO", 1)
	_, e3 := postgres.NewJourneyRepo(pool).Get(ctx, "nope")
	_, e4 := postgres.NewCatalogRepo(pool).Get(ctx, "nope")
	for i, err := range []error{e1, e2, e3, e4} {
		if !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("miss %d = %v, want ports.ErrNotFound", i, err)
		}
	}
}
