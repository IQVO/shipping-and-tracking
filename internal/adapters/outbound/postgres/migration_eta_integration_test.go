//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/repocontract"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// Migration 0003 (ADR 0013): the three ETA columns are null together and a
// window has a positive width, whatever writes to the table.
func TestJourneyEtaColumnsAreAllOrNothing(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	p := repocontract.NewPair(t, 1)
	if err := postgres.NewLabelRepo(pool).Save(ctx, p.Label); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewJourneyRepo(pool).Save(ctx, p.Journey); err != nil {
		t.Fatal(err)
	}
	tn := string(p.Journey.TrackingNumber())
	const set = `UPDATE package_journeys SET eta_start = $2, eta_end = $3, eta_assigned_at = $4 WHERE tracking_number = $1`
	var (
		start = repocontract.Hour(3)
		end   = repocontract.Hour(5)
		none  *struct{}
	)
	for name, args := range map[string][3]any{
		"only a start":     {start, none, none},
		"only an end":      {none, end, none},
		"no assignedAt":    {start, end, none},
		"start after end":  {end, start, repocontract.Hour(2)},
		"start equals end": {start, start, repocontract.Hour(2)},
	} {
		if _, err := pool.Exec(ctx, set, tn, args[0], args[1], args[2]); err == nil {
			t.Errorf("%s: the CHECK must refuse the row", name)
		}
	}
	if _, err := pool.Exec(ctx, set, tn, start, end, repocontract.Hour(2)); err != nil {
		t.Fatalf("a complete window is accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, set, tn, none, none, none); err != nil {
		t.Fatalf("clearing the window is accepted: %v", err)
	}
}
