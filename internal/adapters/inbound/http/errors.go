package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
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

// validationFailed is the one problem of a rejected request: the contract has
// no 400, a malformed body or a bad Idempotency-Key is 422 validation-failed
// (apis/openapi.yaml, ADR 0002 A24); the detail says which.
var validationFailed = problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the idempotency middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = validationFailed
	idempotencyKeyInvalid  = validationFailed
	idempotencyKeyReused   = validationFailed
)

// problemCatalogue maps every typed error to its problem, in match order.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, validationFailed},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Resource not found"}},
	{shared.ErrValidation, validationFailed},
	{ports.ErrShipToUnavailable, problem{http.StatusServiceUnavailable, "ship-to-unavailable", "The ship-to address is unavailable"}},
	{label.ErrLabelAlreadyIssued, problem{http.StatusConflict, "label-already-issued", "A label was already issued for this package"}},
	{label.ErrTrackingNumberTaken, problem{http.StatusConflict, "tracking-number-taken", "The tracking number is already taken"}},
	{journey.ErrVoidAfterHandover, problem{http.StatusConflict, "void-after-handover", "A handed-over package cannot be voided"}},
	{journey.ErrDuplicateScan, problem{http.StatusConflict, "duplicate-scan", "The scan was already recorded"}},
	{journey.ErrScanOutOfOrder, problem{http.StatusConflict, "scan-out-of-order", "The scan is older than the last applied scan"}},
	{journey.ErrJourneyClosed, problem{http.StatusConflict, "journey-closed", "The journey is closed"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "The scan has no edge from the current state"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; retry"}},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
