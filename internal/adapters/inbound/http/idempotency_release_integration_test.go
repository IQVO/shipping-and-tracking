//go:build integration

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// A concurrent duplicate waits on the key row of the first request. When the
// first ends RETRYABLE its transaction rolls back and the key row goes with
// it: the waiting duplicate inserts its own row and EXECUTES the command; it is
// not answered with the refusal it waited for (ADR 0020). This is what a 5xx
// of the first already did.
func TestIdempotency_AConcurrentDuplicateExecutesWhenTheFirstEndsRetryable(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	mw := inbound.RequireIdempotencyKey(pool, func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }, "dup")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
				inbound.WriteErrorForTest(w, r, ports.ErrConcurrentUpdate)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))

	var wg sync.WaitGroup
	var first, dup *httptest.ResponseRecorder
	wg.Add(2)
	go func() { defer wg.Done(); first = serveKeyed(mw, "k-dup") }()
	<-started
	go func() { defer wg.Done(); dup = serveKeyed(mw, "k-dup") }()
	waitForBlockedKeyInsert(t, pool)
	close(release)
	wg.Wait()

	if first.Code != http.StatusConflict || !strings.Contains(first.Body.String(), "concurrent-update") {
		t.Fatalf("first = %d %s, want 409 concurrent-update", first.Code, first.Body.String())
	}
	if dup.Code != http.StatusCreated || dup.Header().Get(inbound.IdempotencyReplayedHeader) != "" || calls.Load() != 2 {
		t.Fatalf("duplicate = %d replayed=%q calls=%d, want a fresh 201 from its own execution", dup.Code, dup.Header().Get(inbound.IdempotencyReplayedHeader), calls.Load())
	}
	if n := poisonCount(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-dup'`); n != 1 {
		t.Fatalf("key rows = %d, want the duplicate's own stored 201", n)
	}
}

func serveKeyed(h http.Handler, key string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, keyedRequest(key))
	return rec
}

// waitForBlockedKeyInsert returns once a request is parked on the key row's
// INSERT ... ON CONFLICT, the state the duplicate waits in.
func waitForBlockedKeyInsert(t *testing.T, pool *pgtest.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if n := poisonCount(t, pool, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%INSERT INTO idempotency_keys%'`); n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the duplicate never blocked on the key row")
}

// A refusal that did not go through writeError has no class: the middleware
// releases it like a RETRYABLE one, the safe fallback (ADR 0020).
func TestIdempotency_AnAnswerWithNoClassIsReleased(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int32
	mw := inbound.RequireIdempotencyKey(pool, pgtx.With, "noclass")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusConflict) // written by hand, no class
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	if first := serveKeyed(mw, "k-noclass"); first.Code != http.StatusConflict {
		t.Fatalf("first = %d, want 409", first.Code)
	}
	if n := poisonCount(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-noclass'`); n != 0 {
		t.Fatalf("%d key rows after an unclassified refusal, want 0 (released)", n)
	}
	if second := serveKeyed(mw, "k-noclass"); second.Code != http.StatusCreated || calls.Load() != 2 {
		t.Fatalf("second = %d calls=%d, want the handler to run again and answer 201", second.Code, calls.Load())
	}
}

// An OUTCOME refusal whose writes were discarded by the savepoint healing
// would answer a lie (the scan it reports is gone): it is neither returned nor
// stored, the same as a 2xx behind an aborted transaction.
func TestIdempotency_AnOutcomeWhoseWritesWereLostIsNeverStored(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int32
	mw := inbound.RequireIdempotencyKey(pool, pgtx.With, "lost")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			poisonTx(t, r)
		}
		inbound.WriteErrorForTest(w, r, shared.ErrIllegalTransition) // OUTCOME
	}))
	if first := serveKeyed(mw, "k-lost"); first.Code != http.StatusInternalServerError {
		t.Fatalf("first = %d %s, want 500: the scan it reports was never written", first.Code, first.Body.String())
	}
	if n := poisonCount(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-lost'`); n != 0 {
		t.Fatalf("%d key rows after a lost OUTCOME, want 0", n)
	}
	if second := serveKeyed(mw, "k-lost"); second.Code != http.StatusConflict || second.Header().Get(inbound.IdempotencyReplayedHeader) != "" {
		t.Fatalf("retry = %d, want the real work to answer its own 409", second.Code)
	}
}
