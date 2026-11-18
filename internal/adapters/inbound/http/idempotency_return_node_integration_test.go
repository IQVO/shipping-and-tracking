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

// keyRows counts the idempotency rows of one key.
func keyRows(t *testing.T, pool *pgtest.Pool, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedReturnsNode is the node fact arriving: node-rc-01 with prefix 07750.
func seedReturnsNode(t *testing.T, pool *pgtest.Pool) {
	t.Helper()
	if err := postgres.NewNodeDestinationRepo(pool).Upsert(context.Background(),
		repository.NodeDestination{NodeID: "node-rc-01", PostalPrefix: "07750"}); err != nil {
		t.Fatal(err)
	}
}

// destination-prefix-unavailable is a refusal the same request can pass later
// (the node fact arrives), so it is NOT stored under the caller's
// Idempotency-Key (ADR 0018): the refusal leaves no key row, the same request
// under the same key is executed again once the node is known, and from then
// on the key replays that success like any other.
func TestReturnLabelDestinationRefusalIsNotStoredSoItsKeyRetriesTheWork(t *testing.T) {
	srv, pool := pgReturnServer(t)
	const key = "ret-1/1"
	body := returnBody("ret-1", `,"destinationNodeId":"node-rc-01"`)

	code1, h1, body1 := postH(t, srv, "/return-labels", key, body)
	if code1 != http.StatusConflict || !strings.Contains(body1, "destination-prefix-unavailable") || h1.Get("Idempotency-Replayed") != "" {
		t.Fatalf("first answer = %d %s (replayed=%q), want a fresh 409 destination-prefix-unavailable", code1, body1, h1.Get("Idempotency-Replayed"))
	}
	if n := keyRows(t, pool, key); n != 0 {
		t.Fatalf("%d idempotency rows of the key after the refusal, want 0: the key insert rolls back with the refusal", n)
	}

	seedReturnsNode(t, pool)

	code2, h2, body2 := postH(t, srv, "/return-labels", key, body)
	if code2 != http.StatusCreated || h2.Get("Idempotency-Replayed") != "" {
		t.Fatalf("same key after the node fact = %d %s (replayed=%q), want the work executed: a fresh 201", code2, body2, h2.Get("Idempotency-Replayed"))
	}
	var prefix string
	if err := pool.QueryRow(context.Background(), `SELECT destination_postal_prefix FROM shipping_labels`).Scan(&prefix); err != nil || prefix != "07750" {
		t.Fatalf("label prefix = %q (%v), want the node's 07750", prefix, err)
	}

	code3, h3, body3 := postH(t, srv, "/return-labels", key, body)
	if code3 != http.StatusCreated || body3 != body2 || h3.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third identical request = %d %s (replayed=%q), want the stored 201 replayed", code3, body3, h3.Get("Idempotency-Replayed"))
	}
	if labels, events, rows := count(t, pool, "shipping_labels"), count(t, pool, "outbox_events"), keyRows(t, pool, key); labels != 1 || events != 1 || rows != 1 {
		t.Fatalf("labels=%d outbox=%d key rows=%d after the replay, want 1/1/1", labels, events, rows)
	}
}

// A response that is not stored leaves the key FREE, not bound to the refused
// request: the key insert is rolled back with everything else, so another
// body under the same key is a new request, not idempotency-key-reused. Once
// a request under the key is stored, the key is bound to it as always.
func TestANonStoredRefusalLeavesItsKeyFreeForAnotherRequest(t *testing.T) {
	srv, pool := pgReturnServer(t)
	const key = "ret-1/1"
	byNode := returnBody("ret-1", `,"destinationNodeId":"node-rc-01"`)
	byPrefix := returnBody("ret-1", `,"destinationPostalPrefix":"01310"`)

	if code, body := post(t, srv, "/return-labels", key, byNode); code != http.StatusConflict || !strings.Contains(body, "destination-prefix-unavailable") {
		t.Fatalf("refusal = %d %s", code, body)
	}
	if code, body := post(t, srv, "/return-labels", key, byPrefix); code != http.StatusCreated {
		t.Fatalf("another body under the same key = %d %s, want it executed as a new request (201)", code, body)
	}
	if code, body := post(t, srv, "/return-labels", key, byNode); code != http.StatusConflict || !strings.Contains(body, "idempotency-key-reused") {
		t.Fatalf("the first body again = %d %s, want 409 idempotency-key-reused: the key now belongs to the stored request", code, body)
	}
	if n := keyRows(t, pool, key); n != 1 {
		t.Fatalf("%d idempotency rows of the key, want 1", n)
	}
}
