//go:build integration

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/clock"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/ids"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// pgReturnServer serves POST /return-labels over a real Postgres, behind the
// real Idempotency-Key middleware and with the real NodeDestinationView.
func pgReturnServer(t *testing.T) (*httptest.Server, *pgtest.Pool) {
	t.Helper()
	pool := pgtest.NewPool(t)
	gw := memory.NewShipToGateway()
	gw.Put("ord-1", shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", Region: "SP", PostalCode: "01310-100", CountryCode: "BR"})
	d := usecases.Deps{
		Labels: postgres.NewLabelRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
		Clock: clock.System{}, IDs: ids.Random{}, ShipTo: gw, StopKeys: memory.HashKeyer{},
		NodeDestinations: postgres.NewNodeDestinationRepo(pool),
	}
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{
		IssueReturnLabel: usecases.NewIssueReturnLabel(d),
		Idempotency: func(route string) func(http.Handler) http.Handler {
			return inbound.RequireIdempotencyKey(pool, pgtx.With, route)
		},
	}, nil, ""))
	t.Cleanup(srv.Close)
	return srv, pool
}

// A characterization, not a wish (ADR 0018, "Known cost"): the middleware
// stores every answer below 500 except concurrent-update, so the
// destination-prefix-unavailable refusal is stored under the caller's key and
// REPLAYED to a retry of that key even after the node fact has arrived, until
// the row is purged (24 hours). The work itself is retryable: a new key is
// served. This PR deliberately leaves that rule alone; when the rule changes,
// this test is the one to flip.
func TestReturnLabelDestinationRefusalIsStoredAndReplayedUnderItsKey(t *testing.T) {
	srv, pool := pgReturnServer(t)
	body := returnBody("ret-1", `,"destinationNodeId":"node-rc-01"`)

	code1, h1, body1 := postH(t, srv, "/return-labels", "ret-1/1", body)
	if code1 != http.StatusConflict || !strings.Contains(body1, "destination-prefix-unavailable") || h1.Get("Idempotency-Replayed") != "" {
		t.Fatalf("first answer = %d %s (replayed=%q), want a fresh 409 destination-prefix-unavailable", code1, body1, h1.Get("Idempotency-Replayed"))
	}
	var stored int
	if err := pool.QueryRow(context.Background(), `SELECT status_code FROM idempotency_keys WHERE key = $1`, "ret-1/1").Scan(&stored); err != nil || stored != http.StatusConflict {
		t.Fatalf("stored outcome = %d (%v), want 409 kept under the key", stored, err)
	}

	// The node fact arrives.
	if err := postgres.NewNodeDestinationRepo(pool).Upsert(context.Background(),
		repository.NodeDestination{NodeID: "node-rc-01", PostalPrefix: "07750"}); err != nil {
		t.Fatal(err)
	}

	code2, h2, body2 := postH(t, srv, "/return-labels", "ret-1/1", body)
	if code2 != http.StatusConflict || body2 != body1 || h2.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("same key after the node fact = %d %s (replayed=%q), want the stored 409 replayed", code2, body2, h2.Get("Idempotency-Replayed"))
	}
	if n := count(t, pool, "shipping_labels"); n != 0 {
		t.Fatalf("%d labels after the replay, want 0", n)
	}

	code3, h3, body3 := postH(t, srv, "/return-labels", "ret-1/1/retry", body)
	if code3 != http.StatusCreated || h3.Get("Idempotency-Replayed") != "" {
		t.Fatalf("a new key after the node fact = %d %s, want 201", code3, body3)
	}
	var prefix string
	if err := pool.QueryRow(context.Background(), `SELECT destination_postal_prefix FROM shipping_labels`).Scan(&prefix); err != nil || prefix != "07750" {
		t.Fatalf("label prefix = %q (%v), want the node's 07750", prefix, err)
	}
}
