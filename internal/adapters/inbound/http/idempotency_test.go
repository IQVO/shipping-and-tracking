package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The transactional Idempotency-Key middleware's Postgres paths are covered by
// idempotency_integration_test.go (they need a real pool). Unit-tested here:
// the request-shape refusals that answer BEFORE the pool is touched, and the
// pure response capture/replay helpers.

// untouchedPool builds a pool that is never acquired: pgxpool.New only parses
// the config (connections are established lazily on Acquire), so the early
// refusals below never dial anything.
func untouchedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://idem:idem@127.0.0.1:1/idem?sslmode=disable")
	if err != nil {
		t.Fatalf("build the never-used pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) (slug string, status int) {
	t.Helper()
	var body struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not a problem document: %v (%s)", err, rec.Body.String())
	}
	return body.Type, rec.Code
}

func idemRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/labels", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestRequireIdempotencyKeyRefusesAMissingKey(t *testing.T) {
	h := RequireIdempotencyKey(untouchedPool(t), func(context.Context, pgx.Tx) context.Context { return context.Background() }, "labels")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the handler must not run without a key")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, idemRequest(`{"poNumber":"PO-1"}`))
	if slug, status := problemOf(t, rec); status != http.StatusBadRequest || slug != "idempotency-key-required" {
		t.Errorf("got %d %q, want 400 idempotency-key-required", status, slug)
	}
}

func TestRequireIdempotencyKeyRefusesATooLongKey(t *testing.T) {
	h := RequireIdempotencyKey(untouchedPool(t), func(context.Context, pgx.Tx) context.Context { return context.Background() }, "labels")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the handler must not run with an over-long key")
	}))
	rec := httptest.NewRecorder()
	r := idemRequest(`{"poNumber":"PO-1"}`)
	r.Header.Set(IdempotencyKeyHeader, strings.Repeat("k", MaxIdempotencyKeyLen+1))
	h.ServeHTTP(rec, r)
	if slug, status := problemOf(t, rec); status != http.StatusUnprocessableEntity || slug != "validation-failed" {
		t.Errorf("got %d %q, want 422 validation-failed", status, slug)
	}
}

// A key of exactly the maximum length passes the shape checks (the request
// then reaches the pool, which cannot connect: an internal error, never a
// cached one). The context deadline bounds the unreachable pool.
func TestRequireIdempotencyKeyAcceptsAMaxLengthKeyUpToThePool(t *testing.T) {
	h := RequireIdempotencyKey(untouchedPool(t), func(context.Context, pgx.Tx) context.Context { return context.Background() }, "labels")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the handler must not run when the pool is unreachable")
	}))
	rec := httptest.NewRecorder()
	r := idemRequest(`{"poNumber":"PO-1"}`)
	r.Header.Set(IdempotencyKeyHeader, strings.Repeat("k", MaxIdempotencyKeyLen))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h.ServeHTTP(rec, r.WithContext(ctx))
	if slug, status := problemOf(t, rec); status != http.StatusInternalServerError || slug != "internal-error" {
		t.Errorf("got %d %q, want 500 internal-error from the unreachable pool", status, slug)
	}
}

func TestCopyStoredHeaders(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want http.Header
	}{
		{"empty", "", http.Header{}},
		{"nil marker", "null", http.Header{}},
		{"one header", `{"Content-Type":["application/json"]}`, http.Header{"Content-Type": []string{"application/json"}}},
		{"multi-value", `{"X-Multi":["a","b"]}`, http.Header{"X-Multi": []string{"a", "b"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := http.Header{}
			copyStoredHeaders(dst, []byte(tc.raw))
			if len(dst) != len(tc.want) {
				t.Fatalf("headers = %v, want %v", dst, tc.want)
			}
			for k, vs := range tc.want {
				if strings.Join(dst.Values(k), ",") != strings.Join(vs, ",") {
					t.Errorf("%s = %v, want %v", k, dst.Values(k), vs)
				}
			}
		})
	}

	// Malformed JSON is ignored, never a 500 on replay.
	dst := http.Header{}
	copyStoredHeaders(dst, []byte("{not json"))
	if len(dst) != 0 {
		t.Errorf("headers = %v, want none from malformed JSON", dst)
	}
}

func TestCaptureRecordsCodeBodyAndDefault(t *testing.T) {
	c := &capture{header: http.Header{}}
	if _, err := c.Write([]byte("body")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if c.code != http.StatusOK {
		t.Errorf("code = %d, want the implicit 200", c.code)
	}
	c.WriteHeader(http.StatusTeapot) // ignored: the first code stands
	if c.code != http.StatusOK {
		t.Errorf("code = %d, want the first recorded code to stand", c.code)
	}
	if c.body.String() != "body" {
		t.Errorf("body = %q", c.body.String())
	}
	if c.noCache {
		t.Error("noCache must start false")
	}
	c.DoNotCache()
	if !c.noCache {
		t.Error("DoNotCache must mark the response retryable")
	}
}

func TestCaptureKeepsTheFirstExplicitCode(t *testing.T) {
	c := &capture{header: http.Header{}}
	c.WriteHeader(http.StatusCreated)
	c.WriteHeader(http.StatusOK)
	if c.code != http.StatusCreated {
		t.Errorf("code = %d, want 201", c.code)
	}
}

func TestFlushCopiesHeadersCodeAndBody(t *testing.T) {
	rec := &capture{header: http.Header{}}
	rec.header.Set("Content-Type", "application/problem+json")
	rec.header.Add("X-Trace", "one")
	rec.header.Add("X-Trace", "two")
	rec.WriteHeader(http.StatusConflict)
	_, _ = rec.Write([]byte(`{"type":"duplicate-scan"}`))

	out := httptest.NewRecorder()
	flush(out, rec)
	if out.Code != http.StatusConflict {
		t.Errorf("code = %d, want 409", out.Code)
	}
	if got := out.Header().Values("X-Trace"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("X-Trace = %v, want both values in order", got)
	}
	if ct := out.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if out.Body.String() != `{"type":"duplicate-scan"}` {
		t.Errorf("body = %q", out.Body.String())
	}
}
