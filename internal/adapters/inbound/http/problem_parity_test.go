package http

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Spec/code parity of the problems (apis/openapi.yaml against the problem
// catalogue). A typed error that reaches a handler without a catalogue entry
// answers 500 internal-error, and a slug a route answers without declaring it
// is a contract the caller cannot read: both stay closed by these tests.
//
// routeProblem is one operation the router serves through writeError and the
// typed errors its handler and its use case can return. The table is kept by
// hand from each use case's "Errors:" comment and the code behind it; the
// tests below tie it to the catalogue, to the spec and to the model's errors.
type routeProblem struct {
	method, path string
	idempotent   bool // wrapped by the Idempotency-Key middleware
	errs         []error
}

var scanRefusals = []error{
	errMalformedRequest, shared.ErrValidation, ports.ErrNotFound, journey.ErrDuplicateScan, journey.ErrScanOutOfOrder,
	journey.ErrJourneyClosed, shared.ErrIllegalTransition, ports.ErrConcurrentUpdate,
}

var routeProblems = []routeProblem{
	{http.MethodPost, "/labels", true, []error{
		errMalformedRequest, shared.ErrValidation, ports.ErrShipToUnavailable, shipto.ErrAddressSuspect,
		shipto.ErrPickupPointUnavailable, label.ErrLabelAlreadyIssued, label.ErrTrackingNumberTaken, ports.ErrConcurrentUpdate,
	}},
	{http.MethodPost, "/return-labels", true, []error{
		errMalformedRequest, shared.ErrValidation, ports.ErrShipToUnavailable, shipto.ErrDestinationPrefixUnavailable,
		label.ErrLabelAlreadyIssued, label.ErrTrackingNumberTaken, ports.ErrConcurrentUpdate,
	}},
	{http.MethodPost, "/labels/{labelRef}/voids", true, []error{
		errMalformedRequest, shared.ErrValidation, ports.ErrNotFound, journey.ErrVoidAfterHandover,
		journey.ErrJourneyClosed, ports.ErrConcurrentUpdate,
	}},
	{http.MethodGet, "/journeys/{trackingNumber}", false, []error{ports.ErrNotFound}},
	{http.MethodPost, "/journeys/{trackingNumber}/scans", true, scanRefusals},
	{http.MethodPost, "/carrier-events", true, scanRefusals},
}

// servedElsewhere are the operations of the spec this router does not serve:
// the analytics read API has its own handler and its own problem.
var servedElsewhere = map[string]bool{
	"GET /reports/package-touches": true, "GET /reports/delivery-outcomes": true, "GET /reports/freshness": true,
}

// middlewareProblems are the problems written in front of every handler.
func middlewareProblems(idempotent bool) []problem {
	if !idempotent {
		return []problem{internalProblem}
	}
	return []problem{internalProblem, idempotencyKeyRequired, idempotencyKeyInvalid, idempotencyKeyReused}
}

// answers is every problem the route can answer, by slug.
func (r routeProblem) answers() map[string]problem {
	out := map[string]problem{}
	for _, p := range middlewareProblems(r.idempotent) {
		out[p.slug] = p
	}
	for _, err := range r.errs {
		p := problemFor(err)
		out[p.slug] = p
	}
	return out
}

func parityDoc(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatalf("load apis/openapi.yaml: %v", err)
	}
	return doc
}

var slugToken = regexp.MustCompile(`[a-z]+(?:-[a-z]+)+`)

// vocabulary is every slug this adapter can write through the catalogue or
// the middleware in front of it.
func vocabulary() map[string]bool {
	out := map[string]bool{}
	for _, p := range middlewareProblems(true) {
		out[p.slug] = true
	}
	for _, entry := range problemCatalogue {
		out[entry.p.slug] = true
	}
	return out
}

// declared returns the slugs of the vocabulary the spec names in the
// description of the operation's response of that status (a $ref resolved).
func declared(op *openapi3.Operation, status int) map[string]bool {
	out := map[string]bool{}
	ref := op.Responses.Status(status)
	if ref == nil || ref.Value == nil || ref.Value.Description == nil {
		return out
	}
	known := vocabulary()
	for _, token := range slugToken.FindAllString(*ref.Value.Description, -1) {
		if known[token] {
			out[token] = true
		}
	}
	return out
}

func operation(t *testing.T, doc *openapi3.T, r routeProblem) *openapi3.Operation {
	t.Helper()
	item := doc.Paths.Find(r.path)
	if item == nil || item.GetOperation(r.method) == nil {
		t.Fatalf("%s %s is not in apis/openapi.yaml", r.method, r.path)
	}
	return item.GetOperation(r.method)
}

// No typed error a route can return falls through to 500, and the spec
// declares its slug on the response of its status, on that route.
func TestEveryRouteDeclaresEveryProblemItCanAnswer(t *testing.T) {
	doc := parityDoc(t)
	for _, r := range routeProblems {
		op := operation(t, doc, r)
		for _, err := range r.errs {
			if problemFor(err) == internalProblem {
				t.Errorf("%s %s: %q has no catalogue entry and answers 500 internal-error", r.method, r.path, err)
			}
		}
		for slug, p := range r.answers() {
			if !declared(op, p.status)[slug] {
				t.Errorf("%s %s answers %d %s, which its %d response does not declare", r.method, r.path, p.status, slug, p.status)
			}
		}
	}
}

// The other direction: the spec promises no problem the route cannot answer.
func TestNoRouteDeclaresAProblemItCannotAnswer(t *testing.T) {
	doc := parityDoc(t)
	for _, r := range routeProblems {
		op := operation(t, doc, r)
		answers := r.answers()
		for code := range op.Responses.Map() {
			status, err := strconv.Atoi(code)
			if err != nil {
				t.Fatalf("%s %s: response code %q is not a status", r.method, r.path, code)
			}
			for _, slug := range unanswerable(declared(op, status), answers, status) {
				t.Errorf("%s %s declares %s on its %s response, which the route cannot answer", r.method, r.path, slug, code)
			}
		}
	}
}

// unanswerable returns the declared slugs the route does not answer with
// that status.
func unanswerable(declared map[string]bool, answers map[string]problem, status int) []string {
	var out []string
	for slug := range declared {
		if p, ok := answers[slug]; !ok || p.status != status {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// Every catalogue entry is reachable: a route returns its error.
func TestEveryCatalogueEntryIsAnsweredBySomeRoute(t *testing.T) {
	reached := map[error]bool{}
	for _, r := range routeProblems {
		for _, err := range r.errs {
			reached[err] = true
		}
	}
	for _, entry := range problemCatalogue {
		if !reached[entry.err] {
			t.Errorf("catalogue entry %s (%q) is returned by no route of the parity table", entry.p.slug, entry.err)
		}
	}
}

// Every operation of the spec is in the table, or named as served elsewhere.
func TestEveryOperationOfTheSpecIsInTheParityTable(t *testing.T) {
	inTable := map[string]bool{}
	for _, r := range routeProblems {
		inTable[r.method+" "+r.path] = true
	}
	var missing []string
	for path, item := range parityDoc(t).Paths.Map() {
		for method := range item.Operations() {
			if key := strings.ToUpper(method) + " " + path; !inTable[key] && !servedElsewhere[key] {
				missing = append(missing, key)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("operations of apis/openapi.yaml without a row in routeProblems: %v", missing)
	}
}
