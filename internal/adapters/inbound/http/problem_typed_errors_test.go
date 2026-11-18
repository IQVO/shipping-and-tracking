package http

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// typedErrors names every sentinel error the model declares (internal/domain
// and internal/application). TestTheTypedErrorsOfTheModelAreAllNamedHere
// keeps the names complete against the sources.
var typedErrors = map[string]error{
	"shared.ErrValidation": shared.ErrValidation, "shared.ErrIllegalTransition": shared.ErrIllegalTransition,
	"shared.ErrInvalidSnapshot": shared.ErrInvalidSnapshot,
	"journey.ErrDuplicateScan":  journey.ErrDuplicateScan, "journey.ErrScanOutOfOrder": journey.ErrScanOutOfOrder,
	"journey.ErrJourneyClosed": journey.ErrJourneyClosed, "journey.ErrVoidAfterHandover": journey.ErrVoidAfterHandover,
	"journey.ErrEtaStale": journey.ErrEtaStale, "journey.ErrPointFlagStale": journey.ErrPointFlagStale,
	"label.ErrLabelAlreadyIssued": label.ErrLabelAlreadyIssued, "label.ErrTrackingNumberTaken": label.ErrTrackingNumberTaken,
	"ports.ErrNotFound": ports.ErrNotFound, "ports.ErrConcurrentUpdate": ports.ErrConcurrentUpdate,
	"ports.ErrShipToUnavailable": ports.ErrShipToUnavailable,
	"shipto.ErrAddressSuspect":   shipto.ErrAddressSuspect, "shipto.ErrPickupPointUnavailable": shipto.ErrPickupPointUnavailable,
	"shipto.ErrDestinationPrefixUnavailable": shipto.ErrDestinationPrefixUnavailable,
}

// notAnHTTPAnswer are the typed errors no route can return, with the reason.
// Everything else in typedErrors must have a catalogue entry.
var notAnHTTPAnswer = map[string]string{
	"journey.ErrEtaStale":       "returned only to the consumed-event fold of an ETA; no route runs that use case",
	"journey.ErrPointFlagStale": "returned only to the consumed-event fold of a point flag; no route runs that use case",
	"shared.ErrInvalidSnapshot": "a stored row that no longer restores is an internal failure: 500 internal-error by design",
}

var sentinelDecl = regexp.MustCompile(`(?m)^(?:var\s+|\s+)(Err[A-Za-z0-9]+)\s*=\s*errors\.New\(`)

// declaredSentinels reads the sentinel errors of the Go sources under root
// (tests excluded), as package.Name.
func declaredSentinels(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range sentinelDecl.FindAllSubmatch(src, -1) {
			out = append(out, filepath.Base(filepath.Dir(path))+"."+string(m[1]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	return out
}

// A new sentinel error of the model fails here until it is named in
// typedErrors, which is what puts it in front of the catalogue test below.
func TestTheTypedErrorsOfTheModelAreAllNamedHere(t *testing.T) {
	var found []string
	for _, root := range []string{"../../../domain", "../../../application"} {
		found = append(found, declaredSentinels(t, root)...)
	}
	sort.Strings(found)
	named := make([]string, 0, len(typedErrors))
	for name := range typedErrors {
		named = append(named, name)
	}
	sort.Strings(named)
	if strings.Join(found, " ") != strings.Join(named, " ") {
		t.Fatalf("sentinel errors declared by the model:\n  %v\nnamed in typedErrors:\n  %v", found, named)
	}
}

// The check that would have caught pickup-point-unavailable answering 500:
// a typed error either has a catalogue entry or is named, with its reason,
// as an error no route can return.
func TestEveryTypedErrorHasACatalogueEntryOrIsNotAnHTTPAnswer(t *testing.T) {
	for name, err := range typedErrors {
		_, excluded := notAnHTTPAnswer[name]
		catalogued := problemFor(err) != internalProblem
		switch {
		case !catalogued && !excluded:
			t.Errorf("%s has no catalogue entry: a handler that returns it answers 500 internal-error", name)
		case catalogued && excluded:
			t.Errorf("%s is named as not an HTTP answer but has the catalogue entry %s", name, problemFor(err).slug)
		}
	}
	for name := range notAnHTTPAnswer {
		if _, ok := typedErrors[name]; !ok {
			t.Errorf("notAnHTTPAnswer names %s, which typedErrors does not hold", name)
		}
	}
}
