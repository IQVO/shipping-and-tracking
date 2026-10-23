//go:build integration

package http_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/clock"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/ids"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

func pgServer(t *testing.T) (*httptest.Server, *pgtest.Pool, *memory.ShipToGateway) {
	t.Helper()
	pool := pgtest.NewPool(t)
	gw := memory.NewShipToGateway()
	gw.Put("ord-1", shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", Region: "SP", PostalCode: "01310-100", CountryCode: "BR"})
	d := usecases.Deps{
		Labels: postgres.NewLabelRepo(pool), Snapshots: postgres.NewSnapshotRepo(pool), Journeys: postgres.NewJourneyRepo(pool),
		Catalog: postgres.NewCatalogRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
		Clock: clock.System{}, IDs: ids.Random{}, ShipTo: gw, Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
	}
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{
		IssueLabel: usecases.NewIssueLabel(d), VoidLabel: usecases.NewVoidLabel(d),
		RecordScan: usecases.NewRecordScan(d), GetJourney: usecases.NewGetJourney(d),
		Idempotency: func(route string) func(http.Handler) http.Handler {
			return inbound.RequireIdempotencyKey(pool, pgtx.With, route)
		},
	}))
	t.Cleanup(srv.Close)
	return srv, pool, gw
}

func post(t *testing.T, srv *httptest.Server, path, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func count(t *testing.T, pool *pgtest.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIdempotencyReplaysTheFirstResponseAndRejectsADifferentBody(t *testing.T) {
	srv, pool, _ := pgServer(t)
	code1, body1 := post(t, srv, "/labels", "key-1", issueBody)
	code2, body2 := post(t, srv, "/labels", "key-1", issueBody)
	if code1 != 201 || code2 != 201 || body1 != body2 {
		t.Fatalf("replay = %d %s / %d %s", code1, body1, code2, body2)
	}
	if n := count(t, pool, "shipping_labels"); n != 1 {
		t.Fatalf("%d labels after a replay, want 1", n)
	}
	if n := count(t, pool, "outbox_events"); n != 1 {
		t.Fatalf("%d outbox rows after a replay, want 1", n)
	}
	code3, body3 := post(t, srv, "/labels", "key-1", strings.Replace(issueBody, "PO-1", "PO-2", 1))
	if code3 != 422 || !strings.Contains(body3, "different request body") {
		t.Fatalf("reuse = %d %s", code3, body3)
	}
}

func TestIdempotencyKeyIsRequiredBoundedAndScopedPerRoute(t *testing.T) {
	srv, _, _ := pgServer(t)
	if code, body := post(t, srv, "/labels", "", issueBody); code != 400 || !strings.Contains(body, "idempotency-key-required") {
		t.Fatalf("missing key = %d %s", code, body)
	}
	if code, body := post(t, srv, "/labels", strings.Repeat("k", 129), issueBody); code != 422 || !strings.Contains(body, "128") {
		t.Fatalf("long key = %d %s", code, body)
	}
	if code, body := post(t, srv, "/labels", strings.Repeat("k", 128), issueBody); code != 201 {
		t.Fatalf("128-char key = %d %s", code, body)
	}
	// The same key on another route is a different request.
	if code, body := post(t, srv, "/labels/nope:void", strings.Repeat("k", 128), `{"reason":"x"}`); code != 404 {
		t.Fatalf("same key, other route = %d %s", code, body)
	}
}

func TestA4xxIsCachedAndAnUnavailableShipToIsNot(t *testing.T) {
	srv, pool, gw := pgServer(t)
	body := strings.Replace(issueBody, "ord-1", "ord-9", 1)
	c1, b1 := post(t, srv, "/labels", "k422", body)
	c2, b2 := post(t, srv, "/labels", "k422", body)
	if c1 != 422 || c2 != 422 || b1 != b2 {
		t.Fatalf("cached 422 = %d %d", c1, c2)
	}

	gw.Fail(true)
	if code, _ := post(t, srv, "/labels", "k503", issueBody); code != 503 {
		t.Fatalf("down = %d", code)
	}
	if n := count(t, pool, "idempotency_keys"); n != 1 {
		t.Fatalf("%d idempotency rows, want only the cached 422", n)
	}
	gw.Fail(false)
	if code, b := post(t, srv, "/labels", "k503", issueBody); code != 201 {
		t.Fatalf("retry with the same key after the 503 = %d %s", code, b)
	}
}

func TestConcurrentRequestsWithOneKeyDoTheWorkOnce(t *testing.T) {
	srv, pool, _ := pgServer(t)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	bodies := make([]string, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], bodies[i] = post(t, srv, "/labels", "same", issueBody)
		}()
	}
	wg.Wait()
	for i := range codes {
		if codes[i] != 201 || bodies[i] != bodies[0] {
			t.Fatalf("request %d = %d %s, want the shared 201", i, codes[i], bodies[i])
		}
	}
	if n := count(t, pool, "shipping_labels"); n != 1 {
		t.Fatalf("%d labels, want 1", n)
	}
}

func TestAStoredOutOfOrderScanSurvivesItsCachedConflict(t *testing.T) {
	srv, pool, _ := pgServer(t)
	_, b := post(t, srv, "/labels", "k-issue", issueBody)
	tn := between(b, `"trackingNumber":"`, `"`)
	path := "/journeys/" + tn + "/scans"
	if code, body := post(t, srv, path, "s1", scanBody("s1", "PICKUP_DEPARTURE", 1)); code != 200 {
		t.Fatalf("scan 1 = %d %s", code, body)
	}
	// An older scan: 409, but the scan is stored unapplied (the unit committed).
	if code, body := post(t, srv, path, "s0", scanBody("s0", "DEPARTURE", 0)); code != 409 || !strings.Contains(body, "scan-out-of-order") {
		t.Fatalf("older scan = %d %s", code, body)
	}
	var stored, applied int
	if err := pool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE applied) FROM journey_scans`).Scan(&stored, &applied); err != nil {
		t.Fatal(err)
	}
	if stored != 2 || applied != 1 {
		t.Fatalf("scans stored %d applied %d, want 2 and 1", stored, applied)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	return s[:strings.Index(s, b)]
}
