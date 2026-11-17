package label

import (
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// newReturnIn is the reverse twin of newIn: no purchase order, no assignment,
// a return authorization instead (ADR 0017).
func newReturnIn() NewLabel {
	return NewLabel{
		Ref: "lbl-ret-1", TrackingNumber: "RN0123456789ABCDEF", PackageNo: 1, OrderID: "ord-1",
		Purpose: PurposeReturn, ReturnAuthorizationID: "ret-1", Tier: shared.TierStandard,
		StopKey: goodStopKey, DestinationPostalPrefix: goodPrefix,
	}
}

func TestIssueReturnLabel(t *testing.T) {
	l, evs, err := Issue(newReturnIn(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if l.Purpose() != PurposeReturn || l.ReturnAuthorizationID() != "ret-1" {
		t.Fatalf("purpose = %s/%s", l.Purpose(), l.ReturnAuthorizationID())
	}
	if l.PONumber() != "" || l.AssignmentID() != "" {
		t.Fatalf("a RETURN label carries no po or assignment: %q %q", l.PONumber(), l.AssignmentID())
	}
	if l.Carrier() != CarrierGround {
		t.Fatalf("a reverse label rides %s, got %s", CarrierGround, l.Carrier())
	}
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev, ok := evs[0].(LabelIssued)
	if !ok {
		t.Fatalf("want LabelIssued, got %T", evs[0])
	}
	if ev.Purpose != PurposeReturn || ev.ReturnAuthorizationID != "ret-1" || ev.Carrier != CarrierGround {
		t.Fatalf("event purpose fields = %s/%s/%s", ev.Purpose, ev.ReturnAuthorizationID, ev.Carrier)
	}
}

func TestReturnLabelRoundTripsThroughTheSnapshot(t *testing.T) {
	l, _, err := Issue(newReturnIn(), t0)
	if err != nil {
		t.Fatal(err)
	}
	// The four places stay one: Issue, Snapshot(), Restore and the persisted
	// Snapshot struct carry the same two fields.
	got, err := Restore(l.Snapshot())
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got.Purpose() != PurposeReturn || got.ReturnAuthorizationID() != "ret-1" {
		t.Fatalf("round trip lost the purpose: %s/%s", got.Purpose(), got.ReturnAuthorizationID())
	}
	if got.Snapshot() != l.Snapshot() {
		t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), l.Snapshot())
	}
}

func TestIssueReturnRefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*NewLabel)
	}{
		{"missing authorization", func(n *NewLabel) { n.ReturnAuthorizationID = "" }},
		{"bad authorization", func(n *NewLabel) { n.ReturnAuthorizationID = "ret 1" }},
		{"with a po", func(n *NewLabel) { n.PONumber = "PO-1" }},
		{"with an assignment", func(n *NewLabel) { n.AssignmentID = "asg-1" }},
		{"bad purpose", func(n *NewLabel) { n.Purpose = "BACK" }},
		{"bad order", func(n *NewLabel) { n.OrderID = strings.Repeat("o", shared.MaxID+1) }},
	}
	for _, c := range cases {
		in := newReturnIn()
		c.mut(&in)
		if _, _, err := Issue(in, t0); err == nil {
			t.Errorf("%s: want a validation error", c.name)
		}
	}
}

func TestOutboundLabelCarriesNoAuthorization(t *testing.T) {
	in := newIn()
	in.ReturnAuthorizationID = "ret-1"
	if _, _, err := Issue(in, t0); err == nil {
		t.Fatal("an OUTBOUND label with a returnAuthorizationId must be refused")
	}
}

func TestPurposeDefaultsToOutbound(t *testing.T) {
	l, evs, err := Issue(newIn(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if l.Purpose() != PurposeOutbound || l.ReturnAuthorizationID() != "" {
		t.Fatalf("default purpose = %s/%s", l.Purpose(), l.ReturnAuthorizationID())
	}
	ev := evs[0].(LabelIssued)
	if ev.Purpose != PurposeOutbound || ev.ReturnAuthorizationID != "" {
		t.Fatalf("event purpose = %s/%s", ev.Purpose, ev.ReturnAuthorizationID)
	}
	// A snapshot written before ADR 0017 (no purpose column value) restores
	// as an outbound label.
	s := l.Snapshot()
	s.Purpose = ""
	got, err := Restore(s)
	if err != nil {
		t.Fatalf("Restore a purpose-less snapshot: %v", err)
	}
	if got.Purpose() != PurposeOutbound {
		t.Fatalf("legacy snapshot purpose = %s", got.Purpose())
	}
}

func TestReturnCarrierMismatchIsCorrupt(t *testing.T) {
	l, err := Restore(func() Snapshot {
		s := issued(t).Snapshot()
		s.Purpose = PurposeReturn
		s.ReturnAuthorizationID = "ret-1"
		s.PONumber, s.AssignmentID = "", ""
		s.Carrier = CarrierExpress
		return s
	}())
	if err == nil {
		t.Fatal("an express reverse label must not restore")
	}
	_ = l
}
