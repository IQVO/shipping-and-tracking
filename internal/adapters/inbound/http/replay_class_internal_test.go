package http

// The replay classes (ADR 0020) without a database: the catalogue declares
// one per problem type, writeError tells the capturing writer, and the
// capture decides from it whether an answer is stored under its key.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Every problem type declares exactly one class and a 5xx is RETRYABLE (it is
// never stored). A new problem type is classified by the PR that adds it.
func TestEveryCatalogueEntryDeclaresAReplayClass(t *testing.T) {
	if len(problemCatalogue) == 0 {
		t.Fatal("the catalogue is empty: the test would pass on nothing")
	}
	for _, e := range problemCatalogue {
		t.Run(e.p.slug, func(t *testing.T) {
			if !e.replay.declared() {
				t.Errorf("%s (%d) declares no replay class (%s): set replayFinal, replayOutcome or replayRetryable on its catalogue entry", e.p.slug, e.p.status, e.replay)
			}
			if e.p.status >= http.StatusInternalServerError && e.replay != replayRetryable {
				t.Errorf("%s answers %d: a 5xx is never stored, so its class is RETRYABLE, got %s", e.p.slug, e.p.status, e.replay)
			}
		})
	}
}

// The check above must bite: the zero value and an unknown value are not classes.
func TestTheUnclassifiedEntryCheckBites(t *testing.T) {
	for _, c := range []replayClass{replayUnset, replayClass(4), replayClass(255)} {
		if c.declared() {
			t.Errorf("class %d is accepted; only FINAL, OUTCOME and RETRYABLE are classes", c)
		}
	}
	for _, c := range []replayClass{replayFinal, replayOutcome, replayRetryable} {
		if !c.declared() {
			t.Errorf("class %s is refused", c)
		}
	}
}

// adrTable reads the class table of ADR 0020: the rows of the form
// "| `slug` | status | CLASS | reason |".
func adrTable(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("../../../../docs/adr/0020-every-problem-type-has-a-replay-class.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| \\d{3} \\| (FINAL|OUTCOME|RETRYABLE) \\|")
	out := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		if prev, dup := out[m[1]]; dup {
			t.Errorf("%s is in the ADR table twice (%s, %s)", m[1], prev, m[2])
		}
		out[m[1]] = m[2]
	}
	return out
}

// One class per problem type: two catalogue entries of the same slug agree,
// and the table of the ADR is the catalogue, slug for slug.
func TestEverySlugHasOneClassAndItIsTheOneOfTheADR(t *testing.T) {
	seen := map[string]string{}
	for _, e := range problemCatalogue {
		if prev, ok := seen[e.p.slug]; ok && prev != e.replay.String() {
			t.Errorf("%s is declared %s and %s by two entries", e.p.slug, prev, e.replay)
		}
		seen[e.p.slug] = e.replay.String()
	}
	want := adrTable(t)
	for slug, class := range seen {
		w, ok := want[slug]
		if !ok {
			t.Errorf("%s is in the catalogue but not in the ADR table: classify it there", slug)
		} else if w != class {
			t.Errorf("%s is %s, the ADR table says %s", slug, class, w)
		}
	}
	for slug := range want {
		if _, ok := seen[slug]; !ok {
			t.Errorf("%s is in the ADR table but not in the catalogue", slug)
		}
	}
}

// writeError tells the capturing writer the class of the problem it writes,
// for every entry of the catalogue; an unmapped error is no problem type.
func TestWriteErrorMarksTheCaptureWithTheClassOfItsProblem(t *testing.T) {
	for _, e := range problemCatalogue {
		c := &capture{header: http.Header{}}
		writeError(c, httptest.NewRequest(http.MethodPost, "/labels", nil), e.err)
		if c.class != e.replay {
			t.Errorf("%s: capture class = %s, want %s", e.p.slug, c.class, e.replay)
		}
		if c.code != e.p.status {
			t.Errorf("%s: status %d, want %d", e.p.slug, c.code, e.p.status)
		}
	}
	c := &capture{header: http.Header{}}
	writeError(c, httptest.NewRequest(http.MethodPost, "/labels", nil), errors.New("boom"))
	if c.class != replayUnset || c.code != http.StatusInternalServerError {
		t.Errorf("an unmapped error: class %s status %d, want UNSET 500", c.class, c.code)
	}
	// A plain writer (a GET, the middlewares) is not a marker and still works.
	rec := httptest.NewRecorder()
	writeError(rec, httptest.NewRequest(http.MethodPost, "/return-labels", nil), shipto.ErrDestinationPrefixUnavailable)
	if rec.Code != http.StatusConflict {
		t.Errorf("plain writer status = %d, want 409", rec.Code)
	}
}

// The behaviour of the former list notStoredUnderKey is the class now: the
// three refusals it named stay released, every other refusal keeps its
// storage, and the two refusals that commit the scan stay stored.
func TestTheFormerNotStoredListIsTheRetryableClass(t *testing.T) {
	cases := []struct {
		err    error
		stored bool
	}{
		{ports.ErrConcurrentUpdate, false},
		{shipto.ErrDestinationPrefixUnavailable, false},
		{shipto.ErrPickupPointUnavailable, false},
		{label.ErrLabelAlreadyIssued, true},
		{journey.ErrDuplicateScan, true},
		{journey.ErrJourneyClosed, true},
		{journey.ErrVoidAfterHandover, true},
		{ports.ErrNotFound, true},
		{journey.ErrScanOutOfOrder, true},
		{shared.ErrIllegalTransition, true},
		{label.ErrTrackingNumberTaken, false},
		{shared.ErrValidation, false},
		{shipto.ErrAddressSuspect, false},
		{ports.ErrShipToUnavailable, false},
	}
	for _, tc := range cases {
		c := &capture{header: http.Header{}}
		writeError(c, httptest.NewRequest(http.MethodPost, "/labels", nil), tc.err)
		if got := c.stored(); got != tc.stored {
			t.Errorf("%v: stored = %v, want %v", tc.err, got, tc.stored)
		}
	}
}

// The scan refusals are the OUTCOMEs: the scan is stored without moving the
// state and the unit commits before the refusal (usecases/scans.go:64-77), so
// they are the only OUTCOME entries.
func TestOnlyTheStoredButNotAppliedScanRefusalsAreOutcomes(t *testing.T) {
	var got []string
	for _, e := range problemCatalogue {
		if e.replay == replayOutcome {
			got = append(got, e.p.slug)
		}
	}
	if len(got) != 2 || got[0] != "scan-out-of-order" || got[1] != "illegal-transition" {
		t.Errorf("OUTCOME entries = %v, want [scan-out-of-order illegal-transition]", got)
	}
}

// What the middleware stores: a 2xx, and a refusal whose class says so. A 5xx
// and a RETRYABLE or unclassified refusal are not; the key is released.
func TestOnlyASuccessAndAFinalOrOutcomeRefusalIsStored(t *testing.T) {
	cases := []struct {
		code  int
		class replayClass
		want  bool
	}{
		{http.StatusOK, replayUnset, true},
		{http.StatusCreated, replayUnset, true},
		{http.StatusConflict, replayFinal, true},
		{http.StatusConflict, replayOutcome, true},
		{http.StatusConflict, replayRetryable, false},
		{http.StatusNotFound, replayFinal, true},
		{http.StatusConflict, replayUnset, false},
		{http.StatusBadGateway, replayFinal, false},
		{http.StatusServiceUnavailable, replayRetryable, false},
		{http.StatusInternalServerError, replayUnset, false},
		{http.StatusInternalServerError, replayOutcome, false},
	}
	for _, tc := range cases {
		c := &capture{header: http.Header{}, code: tc.code}
		c.MarkReplay(tc.class)
		if got := c.stored(); got != tc.want {
			t.Errorf("%d %s: stored = %v, want %v", tc.code, tc.class, got, tc.want)
		}
	}
}
