package ids

import (
	"regexp"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestLabelRefsAreValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		ref := string(Random{}.NewLabelRef())
		if !shared.ValidID(ref) || ref[:4] != "lbl-" || len(ref) != 40 || seen[ref] {
			t.Fatalf("bad or repeated label ref %q", ref)
		}
		seen[ref] = true
	}
}

func TestTrackingNumbersHaveTheDocumentedShape(t *testing.T) {
	rx := regexp.MustCompile(`^RN[A-Z2-7]{16}$`)
	seen := map[string]bool{}
	for range 200 {
		tn := Random{}.NewTrackingNumber()
		if !rx.MatchString(string(tn)) || !tn.Valid() || seen[string(tn)] {
			t.Fatalf("bad or repeated tracking number %q", tn)
		}
		seen[string(tn)] = true
	}
}
