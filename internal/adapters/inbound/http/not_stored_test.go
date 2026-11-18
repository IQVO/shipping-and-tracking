package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

// Which answers the Idempotency-Key middleware does NOT store is decided in
// one place, writeError, for the refusals the same request can pass later:
// the optimistic-lock conflict, the return destination whose node fact has
// not arrived (ADR 0018) and the pickup point that is not ACTIVE yet (ADR
// 0016, addendum of 2026-11-18). Every other typed error of the catalogue, and an
// unmapped one, is left to the middleware's own rule (stored below 500).
func TestWriteErrorMarksOnlyTheRetryableRefusalsAsNotStored(t *testing.T) {
	notStored := map[error]bool{}
	for _, err := range notStoredUnderKey {
		notStored[err] = true
	}
	if len(notStored) != 3 || !notStored[ports.ErrConcurrentUpdate] || !notStored[shipto.ErrDestinationPrefixUnavailable] ||
		!notStored[shipto.ErrPickupPointUnavailable] {
		t.Fatalf("notStoredUnderKey = %v, want exactly the optimistic-lock conflict, the destination refusal and the pickup refusal", notStoredUnderKey)
	}
	errs := []error{errors.New("an error no catalogue entry matches")}
	for _, entry := range problemCatalogue {
		errs = append(errs, entry.err)
	}
	marked := 0
	for _, err := range errs {
		rec := &capture{header: http.Header{}}
		writeError(rec, httptest.NewRequest(http.MethodPost, "/return-labels", nil), fmt.Errorf("wrapped: %w", err))
		if rec.noCache != notStored[err] {
			t.Errorf("%v: marked not-to-store = %v, want %v", err, rec.noCache, notStored[err])
		}
		if rec.noCache {
			marked++
		}
		// The answer itself is the catalogue's, marked or not.
		if want := problemFor(err); rec.code != want.status {
			t.Errorf("%v: status %d, want %d", err, rec.code, want.status)
		}
	}
	if marked != len(notStored) {
		t.Errorf("%d errors of the catalogue are marked, want exactly %d", marked, len(notStored))
	}
}

// A writer that is not the middleware's capture (a handler unit test, a route
// without a key) cannot be marked and is written to as before.
func TestWriteErrorOnAPlainWriterStillAnswersTheProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, httptest.NewRequest(http.MethodPost, "/return-labels", nil), shipto.ErrDestinationPrefixUnavailable)
	if slug, status := problemOf(t, rec); status != http.StatusConflict || slug != "destination-prefix-unavailable" {
		t.Errorf("got %d %q, want 409 destination-prefix-unavailable", status, slug)
	}
}
