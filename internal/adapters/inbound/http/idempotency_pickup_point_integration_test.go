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

// pgPickupServer serves POST /labels over a real Postgres, behind the real
// Idempotency-Key middleware and with the real PickupPointView. Order
// ord-pick is collected at node-pnt-07. The returned use case is the fold of
// node-topology's node facts into that view.
func pgPickupServer(t *testing.T) (*httptest.Server, *pgtest.Pool, *usecases.NotePickupPoint) {
	t.Helper()
	pool := pgtest.NewPool(t)
	gw := memory.NewShipToGateway()
	gw.Put(pickupOrder, shipto.Address{DestinationKind: "PICKUP_POINT", PickupPointID: pickupPoint})
	d := usecases.Deps{
		Labels: postgres.NewLabelRepo(pool), Snapshots: postgres.NewSnapshotRepo(pool), Journeys: postgres.NewJourneyRepo(pool),
		Inbox:  postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
		Clock: clock.System{}, IDs: ids.Random{}, ShipTo: gw, Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
		PickupPoints: postgres.NewPickupPointRepo(pool),
	}
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{
		IssueLabel: usecases.NewIssueLabel(d),
		Idempotency: func(route string) func(http.Handler) http.Handler {
			return inbound.RequireIdempotencyKey(pool, pgtx.With, route)
		},
	}, nil, ""))
	t.Cleanup(srv.Close)
	return srv, pool, usecases.NewNotePickupPoint(d)
}

// pickup-point-unavailable is a refusal the same request can pass later (the
// point is activated), so it is NOT stored under the caller's Idempotency-Key
// (ADR 0016, addendum of 2026-11-18): the refusal leaves no key row, the same
// request under the same key is executed again once the point is ACTIVE, and
// from then on the key replays that success like any other. The key is the
// shape vendor-connectivity sends, deterministic per package.
func TestPickupRefusalIsNotStoredSoItsKeyRetriesTheWork(t *testing.T) {
	srv, pool, fold := pgPickupServer(t)
	ctx := context.Background()
	const key = "vc-label-PO-7-1"
	if err := fold.Execute(ctx, usecases.NotePointCmd{Consumer: "node-topology", EventID: "ev-registered", Point: repository.PickupPoint{
		ID: pickupPoint, StationNodeID: "node-ds-07", PostalPrefix: "07095000", State: repository.PickupPointProposed,
	}}); err != nil {
		t.Fatalf("NodeRegistered: %v", err)
	}

	code1, h1, body1 := postH(t, srv, "/labels", key, pickupBody)
	if code1 != http.StatusUnprocessableEntity || !strings.Contains(body1, slugNoPickupPoint) || h1.Get("Idempotency-Replayed") != "" {
		t.Fatalf("first answer = %d %s (replayed=%q), want a fresh 422 %s", code1, body1, h1.Get("Idempotency-Replayed"), slugNoPickupPoint)
	}
	if n := keyRows(t, pool, key); n != 0 {
		t.Errorf("%d idempotency rows of the key after the refusal, want 0: the key insert rolls back with the refusal", n)
	}

	if err := fold.ExecuteStatus(ctx, usecases.NotePointStatusCmd{
		Consumer: "node-topology", EventID: "ev-activated", NodeID: pickupPoint, State: repository.PickupPointActive,
	}); err != nil {
		t.Fatalf("NodeActivated: %v", err)
	}

	code2, h2, body2 := postH(t, srv, "/labels", key, pickupBody)
	if code2 != http.StatusCreated || h2.Get("Idempotency-Replayed") != "" {
		t.Fatalf("same key after NodeActivated = %d %s (replayed=%q), want the work executed: a fresh 201", code2, body2, h2.Get("Idempotency-Replayed"))
	}
	var prefix string
	if err := pool.QueryRow(ctx, `SELECT destination_postal_prefix FROM shipping_labels`).Scan(&prefix); err != nil || prefix != "07095000" {
		t.Fatalf("label prefix = %q (%v), want the point's 07095000", prefix, err)
	}

	code3, h3, body3 := postH(t, srv, "/labels", key, pickupBody)
	if code3 != http.StatusCreated || body3 != body2 || h3.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third identical request = %d %s (replayed=%q), want the stored 201 replayed", code3, body3, h3.Get("Idempotency-Replayed"))
	}
	if labels, events, rows := count(t, pool, "shipping_labels"), count(t, pool, "outbox_events"), keyRows(t, pool, key); labels != 1 || events != 1 || rows != 1 {
		t.Fatalf("labels=%d outbox=%d key rows=%d after the replay, want 1/1/1", labels, events, rows)
	}
}
