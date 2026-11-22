package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type, a field out of its schema bounds).
var errMalformedRequest = errors.New("request body is not valid JSON for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time. The slug is the model's Err name in
// kebab-case (apis/openapi.yaml, Problem.type).
type problem struct {
	status int
	slug   string
	title  string
}

// validationFailed is the one problem of a rejected request: a malformed body,
// a malformed body is 422 validation-failed (apis/openapi.yaml). The
// Idempotency-Key problems are not 422: missing/malformed keys are 400 and a
// reused key is 409 (ADR 0010, the fleet REST guideline).
var validationFailed = problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the idempotency middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

// catalogueEntry is one typed error, its problem and the replay class of that
// problem (what the Idempotency-Key middleware does with the answer, ADR 0020).
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem and its replay class,
// in match order.
//
// Every entry declares its replay class (ADR 0020 holds the reason of each):
// FINAL when the answer follows the request or a fact that never reverts
// (stored and replayed under the Idempotency-Key), OUTCOME when the command
// committed state and then refused (stored, and the writes commit with the
// stored answer), RETRYABLE when nothing was committed and the answer depends
// on state that can change (not stored, the key is released). A 5xx is
// RETRYABLE and never stored. When unsure the class is RETRYABLE: a released
// refusal costs one more execution, a wrongly FINAL one pins a stale refusal
// for 24 h. A new entry without a class fails
// TestEveryCatalogueEntryDeclaresAReplayClass.
var problemCatalogue = []catalogueEntry{
	{errMalformedRequest, validationFailed, replayRetryable},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Resource not found"}, replayFinal},
	{shared.ErrValidation, validationFailed, replayRetryable},
	{ports.ErrShipToUnavailable, problem{http.StatusServiceUnavailable, "ship-to-unavailable", "The ship-to address is unavailable"}, replayRetryable},
	{shipto.ErrAddressSuspect, problem{http.StatusUnprocessableEntity, "address-suspect", "The ship-to address failed the deliverability check; no label is stored"}, replayRetryable},
	{shipto.ErrPickupPointUnavailable, problem{http.StatusUnprocessableEntity, "pickup-point-unavailable", "The order's pickup point is unknown to this context or not ACTIVE; no label is stored"}, replayRetryable},
	{label.ErrLabelAlreadyIssued, problem{http.StatusConflict, "label-already-issued", "A label was already issued for this package"}, replayFinal},
	{shipto.ErrDestinationPrefixUnavailable, problem{http.StatusConflict, "destination-prefix-unavailable", "This context holds no usable destination prefix for the destination node; no label is stored"}, replayRetryable},
	{label.ErrTrackingNumberTaken, problem{http.StatusConflict, "tracking-number-taken", "The tracking number is already taken"}, replayRetryable},
	{journey.ErrVoidAfterHandover, problem{http.StatusConflict, "void-after-handover", "A handed-over package cannot be voided"}, replayFinal},
	{journey.ErrDuplicateScan, problem{http.StatusConflict, "duplicate-scan", "The scan was already recorded"}, replayFinal},
	{journey.ErrScanOutOfOrder, problem{http.StatusConflict, "scan-out-of-order", "The scan is older than the last applied scan"}, replayOutcome},
	{journey.ErrJourneyClosed, problem{http.StatusConflict, "journey-closed", "The journey is closed"}, replayFinal},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "The scan has no edge from the current state"}, replayOutcome},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; retry"}, replayRetryable},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem { return entryFor(err).p }

// entryFor returns the first catalogue entry err matches; an error the
// catalogue does not know is the internal-error problem with no class.
func entryFor(err error) catalogueEntry {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry
		}
	}
	return catalogueEntry{p: internalProblem}
}

// replayOf returns the replay class of the problem err is answered with.
func replayOf(err error) replayClass { return entryFor(err).replay }
