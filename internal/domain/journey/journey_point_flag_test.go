package journey

import (
	"errors"
	"testing"
	"time"
)

var (
	flagAt  = time.Date(2026, 10, 24, 16, 30, 0, 0, time.UTC)
	flagNow = time.Date(2026, 10, 24, 16, 30, 5, 0, time.UTC)
)

func flaggedJourney(t *testing.T) *Journey {
	t.Helper()
	j, err := Start(NewJourney{TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-1", PONumber: "po-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: "STANDARD"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestFlagDeliveryPointStoresTheFlagAndRaisesTheEvent(t *testing.T) {
	j := flaggedJourney(t)
	events, err := j.FlagDeliveryPoint("NOT_HOME", flagAt, flagNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	ev, ok := events[0].(PackageDeliveryPointFlagged)
	if !ok || ev.EventName() != "PackageDeliveryPointFlagged" || ev.Entity() != "journey" || ev.SubjectID() != "RN0123456789ABCDEF" {
		t.Fatalf("event = %+v", events[0])
	}
	if ev.Reason != "NOT_HOME" || !ev.FlaggedAt.Equal(flagAt) || ev.OrderID != "ord-1" {
		t.Fatalf("event = %+v", ev)
	}
	if j.PointFlagReason() != "NOT_HOME" || !j.PointFlaggedAt().Equal(flagAt) {
		t.Fatalf("journey = %q %v", j.PointFlagReason(), j.PointFlaggedAt())
	}
	s := j.Snapshot()
	if s.PointFlagReason != "NOT_HOME" || !s.PointFlaggedAt.Equal(flagAt) {
		t.Fatalf("snapshot = %+v", s)
	}
	if _, err := Restore(s); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func TestFlagDeliveryPointRefusesBadReasonMissingTimeAndTerminal(t *testing.T) {
	j := flaggedJourney(t)
	if _, err := j.FlagDeliveryPoint("SNOWSTORM", flagAt, flagNow); err == nil {
		t.Fatal("an unpublished reason must be refused")
	}
	if _, err := j.FlagDeliveryPoint("NOT_HOME", time.Time{}, flagNow); err == nil {
		t.Fatal("a missing flaggedAt must be refused")
	}
	terminal, err := Start(NewJourney{TrackingNumber: "RN0123456789ABCDFF", LabelRef: "lbl-2", PONumber: "po-2",
		AssignmentID: "asg-2", OrderID: "ord-2", Tier: "STANDARD"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.RecordScan(ScanInput{ID: "s-1", NodeID: "node-1", Type: ScanPickupDeparture, At: time.Now()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.RecordScan(ScanInput{ID: "s-2", NodeID: "node-1", Type: ScanStationArrival, At: time.Now()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.ReturnToSender(time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.FlagDeliveryPoint("NOT_HOME", flagAt, flagNow); !errors.Is(err, ErrJourneyClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestFlagDeliveryPointNewestWins(t *testing.T) {
	j := flaggedJourney(t)
	if _, err := j.FlagDeliveryPoint("NOT_HOME", flagAt, flagNow); err != nil {
		t.Fatal(err)
	}
	if _, err := j.FlagDeliveryPoint("ACCESS_PROBLEM", flagAt.Add(-time.Minute), flagNow); !errors.Is(err, ErrPointFlagStale) {
		t.Fatalf("an older flag must be stale: %v", err)
	}
	if j.PointFlagReason() != "NOT_HOME" {
		t.Fatalf("reason = %q", j.PointFlagReason())
	}
	if _, err := j.FlagDeliveryPoint("ACCESS_PROBLEM", flagAt.Add(time.Minute), flagNow); err != nil {
		t.Fatal(err)
	}
	if j.PointFlagReason() != "ACCESS_PROBLEM" {
		t.Fatalf("reason = %q", j.PointFlagReason())
	}
}

func TestCheckSnapshotRefusesHalfSetAndBadReasonFlags(t *testing.T) {
	base := flaggedJourney(t).Snapshot()
	half := base
	half.PointFlaggedAt = flagAt
	if _, err := Restore(half); err == nil {
		t.Fatal("a flag without a reason must be refused")
	}
	bad := base
	bad.PointFlagReason, bad.PointFlaggedAt = "SNOWSTORM", flagAt
	if _, err := Restore(bad); err == nil {
		t.Fatal("an unpublished reason must be refused")
	}
}
