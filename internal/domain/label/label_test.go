package label

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

const (
	goodStopKey = "9f2a6c0d4b7e11aa5c3d8e90b1f47a62c8d0e3f15a9b7c24d6e8f0a1b3c5d7e9"
	goodPrefix  = "01310"
)

func newIn() NewLabel {
	return NewLabel{
		Ref: "lbl-1", TrackingNumber: "RN0123456789ABCDEF", PONumber: "PO-1", PackageNo: 1,
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay, StopKey: goodStopKey,
		DestinationPostalPrefix: goodPrefix,
	}
}

func issued(t *testing.T) *ShippingLabel {
	t.Helper()
	l, _, err := Issue(newIn(), t0)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return l
}

func TestIssueRaisesLabelIssuedAtVersionOne(t *testing.T) {
	l, evs, err := Issue(newIn(), t0.In(time.FixedZone("x", 3600)))
	if err != nil {
		t.Fatal(err)
	}
	assertIssued(t, l)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev, ok := evs[0].(LabelIssued)
	if !ok {
		t.Fatalf("want LabelIssued, got %T", evs[0])
	}
	assertIssuedEvent(t, ev)
}

func assertIssued(t *testing.T, l *ShippingLabel) {
	t.Helper()
	checks := []struct {
		name      string
		got, want any
	}{
		{"ref", l.Ref(), Ref("lbl-1")}, {"tracking", l.TrackingNumber(), shared.TrackingNumber("RN0123456789ABCDEF")},
		{"po", l.PONumber(), "PO-1"}, {"packageNo", l.PackageNo(), 1}, {"assignment", l.AssignmentID(), "asg-1"},
		{"order", l.OrderID(), "ord-1"}, {"tier", l.Tier(), shared.TierNextDay}, {"carrier", l.Carrier(), CarrierExpress},
		{"stopKey", l.StopKey(), goodStopKey}, {"prefix", l.DestinationPostalPrefix(), goodPrefix},
		{"status", l.Status(), StatusIssued}, {"issuedAt", l.IssuedAt(), t0}, {"version", l.Version(), int64(1)},
		{"voidReason", l.VoidReason(), ""}, {"voidedAt", l.VoidedAt(), time.Time{}},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if l.IssuedAt().Location() != time.UTC {
		t.Error("issuedAt must be UTC")
	}
}

func assertIssuedEvent(t *testing.T, ev LabelIssued) {
	t.Helper()
	if ev.EventName() != "LabelIssued" || ev.Entity() != "label" || ev.SubjectID() != "lbl-1" {
		t.Errorf("event identity = %s/%s/%s", ev.EventName(), ev.Entity(), ev.SubjectID())
	}
	if ev.AggregateVersion() != 1 || !ev.OccurredAt().Equal(t0) {
		t.Errorf("event header = %d/%v", ev.AggregateVersion(), ev.OccurredAt())
	}
	if ev.TrackingNumber != "RN0123456789ABCDEF" || ev.PONumber != "PO-1" || ev.AssignmentID != "asg-1" ||
		ev.OrderID != "ord-1" || ev.PackageNo != 1 || ev.Tier != shared.TierNextDay || ev.Carrier != CarrierExpress ||
		ev.StopKey != goodStopKey || ev.DestinationPostalPrefix != goodPrefix {
		t.Errorf("event data = %+v", ev)
	}
}

func TestIssueRefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*NewLabel)
	}{
		{"bad ref", func(n *NewLabel) { n.Ref = "" }},
		{"bad tracking", func(n *NewLabel) { n.TrackingNumber = "a b" }},
		{"bad po", func(n *NewLabel) { n.PONumber = "" }},
		{"bad assignment", func(n *NewLabel) { n.AssignmentID = " x" }},
		{"bad order", func(n *NewLabel) { n.OrderID = strings.Repeat("o", shared.MaxID+1) }},
		{"package zero", func(n *NewLabel) { n.PackageNo = 0 }},
		{"package negative", func(n *NewLabel) { n.PackageNo = -1 }},
		{"package over max", func(n *NewLabel) { n.PackageNo = MaxPackageNo + 1 }},
		{"bad tier", func(n *NewLabel) { n.Tier = "EXPRESS" }},
		{"short stopKey", func(n *NewLabel) { n.StopKey = goodStopKey[:63] }},
		{"long stopKey", func(n *NewLabel) { n.StopKey += "0" }},
		{"upper stopKey", func(n *NewLabel) { n.StopKey = strings.ToUpper(goodStopKey) }},
		{"non hex stopKey", func(n *NewLabel) { n.StopKey = strings.Repeat("g", StopKeyLen) }},
		{"empty prefix", func(n *NewLabel) { n.DestinationPostalPrefix = "" }},
		{"long prefix", func(n *NewLabel) { n.DestinationPostalPrefix = strings.Repeat("1", MaxPostalPrefix+1) }},
		{"lower prefix", func(n *NewLabel) { n.DestinationPostalPrefix = "ab1" }},
		{"dash prefix", func(n *NewLabel) { n.DestinationPostalPrefix = "01-310" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := newIn()
			c.mut(&in)
			l, evs, err := Issue(in, t0)
			if !errors.Is(err, shared.ErrValidation) || l != nil || evs != nil {
				t.Fatalf("want ErrValidation and nothing built, got %v %v %v", l, evs, err)
			}
		})
	}
}

func TestIssueAcceptsTheBounds(t *testing.T) {
	in := newIn()
	in.PackageNo = MaxPackageNo
	in.DestinationPostalPrefix = strings.Repeat("A", MaxPostalPrefix)
	in.PONumber = strings.Repeat("p", shared.MaxID)
	if _, _, err := Issue(in, t0); err != nil {
		t.Fatalf("bounds must be accepted: %v", err)
	}
	in.PackageNo = 1
	if _, _, err := Issue(in, t0); err != nil {
		t.Fatalf("package 1 must be accepted: %v", err)
	}
}

func TestCarrierByTier(t *testing.T) {
	for tier, want := range map[shared.Tier]string{
		shared.TierSameDay: CarrierExpress, shared.TierNextDay: CarrierExpress,
		shared.TierTwoDay: CarrierGround, shared.TierStandard: CarrierGround,
	} {
		if got := CarrierFor(tier); got != want {
			t.Errorf("CarrierFor(%s) = %s, want %s", tier, got, want)
		}
	}
}

func TestVoidMovesIssuedToVoided(t *testing.T) {
	l := issued(t)
	evs, err := l.Void("order cancelled", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if l.Status() != StatusVoided || l.VoidReason() != "order cancelled" || !l.VoidedAt().Equal(t0.Add(time.Hour)) || l.Version() != 2 {
		t.Fatalf("after void: %s %q %v v%d", l.Status(), l.VoidReason(), l.VoidedAt(), l.Version())
	}
	ev, ok := evs[0].(LabelVoided)
	if len(evs) != 1 || !ok {
		t.Fatalf("want one LabelVoided, got %v", evs)
	}
	if ev.EventName() != "LabelVoided" || ev.Entity() != "label" || ev.SubjectID() != "lbl-1" || ev.AggregateVersion() != 2 ||
		ev.TrackingNumber != "RN0123456789ABCDEF" || ev.Reason != "order cancelled" || !ev.OccurredAt().Equal(t0.Add(time.Hour)) {
		t.Fatalf("event = %+v", ev)
	}
}

func TestVoidTwiceIsAnIllegalTransition(t *testing.T) {
	l := issued(t)
	if _, err := l.Void("first", t0); err != nil {
		t.Fatal(err)
	}
	evs, err := l.Void("second", t0)
	if !errors.Is(err, shared.ErrIllegalTransition) || evs != nil || l.VoidReason() != "first" || l.Version() != 2 {
		t.Fatalf("second void: %v %v reason=%q v%d", evs, err, l.VoidReason(), l.Version())
	}
}

func TestVoidRefusesABadReasonWithoutChange(t *testing.T) {
	for _, reason := range []string{"", " ", " padded", "bell\x07", strings.Repeat("r", MaxReason+1)} {
		l := issued(t)
		if _, err := l.Void(reason, t0); !errors.Is(err, shared.ErrValidation) || l.Status() != StatusIssued || l.Version() != 1 {
			t.Errorf("reason %q: err=%v status=%s v%d", reason, err, l.Status(), l.Version())
		}
	}
	l := issued(t)
	if _, err := l.Void(strings.Repeat("r", MaxReason), t0); err != nil {
		t.Fatalf("a reason of exactly MaxReason characters must be accepted: %v", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	l := issued(t)
	got, err := Restore(l.Snapshot())
	if err != nil || got.Snapshot() != l.Snapshot() {
		t.Fatalf("issued round trip: %v", err)
	}
	if _, err := l.Void("gone", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = Restore(l.Snapshot())
	if err != nil || got.Snapshot() != l.Snapshot() {
		t.Fatalf("voided round trip: %v", err)
	}
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	good := issued(t).Snapshot()
	voided := good
	voided.Status, voided.VoidReason, voided.VoidedAt, voided.Version = StatusVoided, "gone", t0.Add(time.Minute), 2
	cases := []struct {
		name string
		base Snapshot
		mut  func(*Snapshot)
	}{
		{"bad ref", good, func(s *Snapshot) { s.Ref = "" }},
		{"bad package", good, func(s *Snapshot) { s.PackageNo = 0 }},
		{"bad stopKey", good, func(s *Snapshot) { s.StopKey = "x" }},
		{"carrier mismatch", good, func(s *Snapshot) { s.Carrier = CarrierGround }},
		{"no issuedAt", good, func(s *Snapshot) { s.IssuedAt = time.Time{} }},
		{"version zero", good, func(s *Snapshot) { s.Version = 0 }},
		{"issued with reason", good, func(s *Snapshot) { s.VoidReason = "x" }},
		{"issued with voidedAt", good, func(s *Snapshot) { s.VoidedAt = t0 }},
		{"unknown status", good, func(s *Snapshot) { s.Status = "LOST" }},
		{"voided without reason", voided, func(s *Snapshot) { s.VoidReason = "" }},
		{"voided without voidedAt", voided, func(s *Snapshot) { s.VoidedAt = time.Time{} }},
		{"voided at version one", voided, func(s *Snapshot) { s.Version = 1 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.base
			c.mut(&s)
			if l, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) || l != nil {
				t.Fatalf("want ErrInvalidSnapshot, got %v %v", l, err)
			}
		})
	}
	if _, err := Restore(voided); err != nil {
		t.Fatalf("a well-formed voided snapshot must restore: %v", err)
	}
	atTwo := voided
	atTwo.Version = 2
	if _, err := Restore(atTwo); err != nil {
		t.Fatalf("version exactly 2 must restore: %v", err)
	}
	one := good
	one.Version = 1
	if _, err := Restore(one); err != nil {
		t.Fatalf("version exactly 1 must restore: %v", err)
	}
}

func TestInvariantErrorsAreDistinct(t *testing.T) {
	if errors.Is(ErrLabelAlreadyIssued, ErrTrackingNumberTaken) || ErrLabelAlreadyIssued.Error() == ErrTrackingNumberTaken.Error() {
		t.Fatal("the two invariant errors must be distinct")
	}
}

func TestPrefixAndHexCharacterBoundaries(t *testing.T) {
	for _, ok := range []string{"0", "9", "A", "Z", "09AZ"} {
		in := newIn()
		in.DestinationPostalPrefix = ok
		if _, _, err := Issue(in, t0); err != nil {
			t.Errorf("prefix %q must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"/", ":", "@", "[", "`", "{", "a", "z"} {
		in := newIn()
		in.DestinationPostalPrefix = bad
		if _, _, err := Issue(in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("prefix %q must be refused", bad)
		}
	}
	for _, ok := range []string{"0", "9", "a", "f"} {
		in := newIn()
		in.StopKey = strings.Repeat(ok, StopKeyLen)
		if _, _, err := Issue(in, t0); err != nil {
			t.Errorf("stopKey of %q must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"/", ":", "`", "g", "A", "F"} {
		in := newIn()
		in.StopKey = strings.Repeat(bad, StopKeyLen)
		if _, _, err := Issue(in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("stopKey of %q must be refused", bad)
		}
	}
}
