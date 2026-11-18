package label

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestIssueCarriesTheDestinationStationOnTheEventOnly(t *testing.T) {
	in := newIn()
	in.DestinationStationNodeID = "node-ds-07"
	l, evs, err := Issue(in, t0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	ev := evs[0].(LabelIssued)
	if ev.DestinationStationNodeID != "node-ds-07" {
		t.Fatalf("event station = %q, want node-ds-07", ev.DestinationStationNodeID)
	}
	if ev.DestinationPostalPrefix != goodPrefix || ev.LabelRef != l.Ref() {
		t.Fatalf("the rest of the event changed: %+v", ev)
	}
}

func TestIssueWithoutAStationRaisesNone(t *testing.T) {
	_, evs, err := Issue(newIn(), t0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := evs[0].(LabelIssued).DestinationStationNodeID; got != "" {
		t.Fatalf("event station = %q, want none", got)
	}
}

func TestIssueRefusesAStationThatIsNotAnId(t *testing.T) {
	for _, bad := range []string{" ", "-x", "has space", strings.Repeat("a", shared.MaxID+1), "Rua das Flores 10"} {
		in := newIn()
		in.DestinationStationNodeID = bad
		if _, _, err := Issue(in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("station %q: err = %v, want a validation error", bad, err)
		}
	}
	in := newIn()
	in.DestinationStationNodeID = strings.Repeat("a", shared.MaxID)
	if _, _, err := Issue(in, t0); err != nil {
		t.Fatalf("a station of the longest id is accepted: %v", err)
	}
}
