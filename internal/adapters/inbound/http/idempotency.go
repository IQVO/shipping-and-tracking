package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdempotencyKeyHeader is the request header a caller must supply on a route
// wrapped by RequireIdempotencyKey.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotencyReplayedHeader is set to "true" on, and only on, a replayed answer.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// MaxIdempotencyKeyLen is the longest accepted key (apis/openapi.yaml).
const MaxIdempotencyKeyLen = 128

// validIdempotencyKey reports whether key is 1-128 printable ASCII characters.
func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > MaxIdempotencyKeyLen {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// TxBinder returns a child context carrying tx, so the use case's own unit of
// work (the Postgres adapter's) JOINS the transaction the middleware opened
// instead of beginning a second one. cmd/ passes the outbound adapter's binder
// in: adapters never import each other.
type TxBinder func(ctx context.Context, tx pgx.Tx) context.Context

var errMissingOutcome = errors.New("idempotency row committed with no recorded outcome")

// RequireIdempotencyKey is the transactional Idempotency-Key middleware, one
// instance per route name. The key is GLOBAL: the row's primary key is the key
// alone, and a request is fingerprinted by method + path + SHA-256 of the body,
// so the same key on another route or body is 409 idempotency-key-reused. The
// route name is stored for operators only.
//
//   - status_code starts NULL and is set by the SAME transaction that inserted
//     the row, immediately before it commits: a committed row always carries
//     its outcome.
//   - Two concurrent requests with the same key race on INSERT ... ON CONFLICT
//     DO NOTHING; the second blocks on the primary key until the first
//     resolves, so "0 rows inserted" means the original transaction finished.
//   - The wrapped handler runs with the transaction bound into its context, so
//     the idempotency row, the aggregates and the outbox rows commit or roll
//     back together.
//   - Every response below 500 is cached (4xx included): the same key and body
//     replays the same answer, a different method, path or body is 409 idempotency-key-reused.
//     A 5xx or a panic rolls everything back and is never cached.
func RequireIdempotencyKey(pool *pgxpool.Pool, bind TxBinder, route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyKeyHeader)
			if key == "" {
				writeProblem(w, r, idempotencyKeyRequired, "send an Idempotency-Key header with a unique value per logical request")
				return
			}
			if !validIdempotencyKey(key) {
				writeProblem(w, r, idempotencyKeyInvalid, "the Idempotency-Key must be 1-128 printable ASCII characters")
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
			if err != nil {
				writeError(w, r, errMalformedRequest)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(body)
			requestHash := r.Method + " " + r.URL.Path + " " + hex.EncodeToString(sum[:])

			ctx := r.Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				writeError(w, r, err)
				return
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO idempotency_keys (route, key, request_hash)
				VALUES ($1, $2, $3)
				ON CONFLICT (key) DO NOTHING
			`, route, key, requestHash)
			if err != nil {
				_ = tx.Rollback(ctx)
				writeError(w, r, err)
				return
			}
			if tag.RowsAffected() == 0 {
				_ = tx.Rollback(ctx)
				replayCachedResponse(w, r, pool, key, requestHash)
				return
			}
			runFreshRequest(ctx, w, r, tx, bind, key, next)
		})
	}
}

// replayCachedResponse answers a request whose key already has a committed row:
// the stored outcome when the fingerprint matches, 409 when it does not.
func replayCachedResponse(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, key, requestHash string) {
	var (
		storedHash string
		statusCode *int
		body       []byte
		headersRaw []byte
	)
	err := pool.QueryRow(r.Context(), `
		SELECT request_hash, status_code, response_body, response_headers
		FROM idempotency_keys WHERE key = $1
	`, key).Scan(&storedHash, &statusCode, &body, &headersRaw)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if storedHash != requestHash {
		writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was already used with a different request")
		return
	}
	if statusCode == nil {
		writeError(w, r, errMissingOutcome)
		return
	}
	copyStoredHeaders(w.Header(), headersRaw)
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(*statusCode)
	_, _ = w.Write(body)
}

func copyStoredHeaders(dst http.Header, raw []byte) {
	if len(raw) == 0 {
		return
	}
	var stored http.Header
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	for k, vs := range stored {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// capture records the handler's response so it can be persisted and committed
// BEFORE anything reaches the client.
type capture struct {
	header  http.Header
	code    int
	body    bytes.Buffer
	noCache bool
}

// DoNotCache marks the response as retryable: the middleware rolls the
// transaction back instead of recording the outcome (a concurrent-update 409
// must not be replayed to a client that retries with the same key).
func (c *capture) DoNotCache() { c.noCache = true }

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

// runFreshRequest handles a genuinely new key: capture the response, persist
// the outcome in the same transaction, commit, and only then write to the real
// client.
//
// The handler runs behind a SAVEPOINT. A mapped domain conflict -- a racing
// label losing on shipping_labels_tracking_key, say -- fails a statement on
// the server and leaves the transaction ABORTED even though the handler
// answers a truthful 4xx. Releasing the savepoint exposes that: the release
// fails, ROLLBACK TO SAVEPOINT discards the half-done work and heals the
// transaction, and the 4xx outcome is recorded and committed like any other.
// A 2xx answer behind a failed release is a lie (its writes are gone), so it
// rolls everything back and fails the request instead.
func runFreshRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, tx pgx.Tx, bind TxBinder, key string, next http.Handler) {
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if _, err := tx.Exec(ctx, "SAVEPOINT handler_run"); err != nil {
		_ = tx.Rollback(ctx)
		writeError(w, r, err)
		return
	}
	rec := &capture{header: http.Header{}}
	next.ServeHTTP(rec, r.WithContext(bind(ctx, tx)))
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	if rec.code >= http.StatusInternalServerError || rec.noCache {
		// A server failure (or a retryable conflict) is never cached: roll everything back so the same
		// key can retry the real work.
		_ = tx.Rollback(ctx)
		flush(w, rec)
		return
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT handler_run"); err != nil {
		// A statement inside the handler failed on the server: the
		// transaction is aborted. Rolling back to the savepoint discards the
		// handler's partial writes -- the correct all-or-nothing semantics
		// for an error answer -- and returns the transaction to health.
		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT handler_run"); rerr != nil {
			_ = tx.Rollback(ctx)
			writeError(w, r, err)
			return
		}
		if rec.code < http.StatusBadRequest {
			// A success answer whose writes cannot be committed must neither
			// be returned nor cached: fail the request so the key retries.
			_ = tx.Rollback(ctx)
			writeError(w, r, err)
			return
		}
	}
	headers, err := json.Marshal(rec.header)
	if err != nil {
		_ = tx.Rollback(ctx)
		writeError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET status_code = $1, response_body = $2, response_headers = $3, completed_at = now()
		WHERE key = $4
	`, rec.code, rec.body.Bytes(), headers, key); err != nil {
		_ = tx.Rollback(ctx)
		writeError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, r, err)
		return
	}
	flush(w, rec)
}

// flush copies the captured response onto the real client connection.
func flush(w http.ResponseWriter, rec *capture) {
	for k, vs := range rec.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}
