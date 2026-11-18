package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

// maxBodyBytes bounds a request body; every body here is a few hundred bytes.
const maxBodyBytes = 64 << 10

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 9457 application/problem+json body. Type is the
// problem slug, a relative URI reference (apis/openapi.yaml, Problem.type).
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON strictly decodes r's body into dest: unknown fields, trailing data,
// malformed JSON and an empty body are errMalformedRequest (every body here is
// required).
func decodeJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("%w: %w", errMalformedRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after the JSON body", errMalformedRequest)
	}
	return nil
}

// notStoredUnderKey lists the refusals the Idempotency-Key middleware must NOT
// store: the same request, under the same key, can pass later, so a stored
// answer would pin a refusal that is no longer true for 24 hours. A success
// and a refusal that is final for the request are not here and stay stored.
// For a listed refusal the handler marks its writer (capture.DoNotCache) and
// the middleware rolls its whole transaction back, the key row included: the
// key is free again, for the same request or for another one.
//
//   - ports.ErrConcurrentUpdate: another writer won the optimistic lock; the
//     retry reloads and may win.
//   - shipto.ErrDestinationPrefixUnavailable: the destination node's
//     registration has not been consumed yet; it commits nothing and the
//     retry is served once the node fact has arrived (ADR 0018).
//   - shipto.ErrPickupPointUnavailable: the order's pickup point is not held
//     ACTIVE yet; it commits nothing and the retry is served once the point's
//     NodeRegistered and NodeActivated have been consumed. A RETIRED point is
//     refused again on every retry, which is harmless (ADR 0016, addendum of
//     2026-11-18).
var notStoredUnderKey = []error{
	ports.ErrConcurrentUpdate,
	shipto.ErrDestinationPrefixUnavailable,
	shipto.ErrPickupPointUnavailable,
}

// passesLater reports whether err is one of the notStoredUnderKey refusals.
func passesLater(err error) bool {
	for _, target := range notStoredUnderKey {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// writeError writes err as its RFC 7807 problem. An unmapped error is a 500
// whose detail never leaks the internal message (it is logged instead).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	if nc, ok := w.(interface{ DoNotCache() }); ok && passesLater(err) {
		nc.DoNotCache()
	}
	detail := err.Error()
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", sanitizeLog(err.Error()), "method", sanitizeLog(r.Method), "path", sanitizeLog(r.URL.Path))
		detail = "an unexpected error occurred"
	}
	writeProblem(w, r, p, detail)
}

// writeProblem writes p with an explicit detail.
func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type:     p.slug,
		Title:    p.title,
		Status:   p.status,
		Detail:   detail,
		Instance: r.URL.EscapedPath(),
	})
}
