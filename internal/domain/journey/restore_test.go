package journey

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var allStates = []State{StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery,
	StateDelivered, StateReturned, StateLost, StateVoided}

func TestSnapshotRestoreRoundTripInEveryState(t *testing.T) {
	for _, st := range allStates {
		t.Run(string(st), func(t *testing.T) {
			j := inState(t, st)
			got, err := Restore(j.Snapshot())
			mustNoErr(t, err)
			if !reflect.DeepEqual(got.Snapshot(), j.Snapshot()) {
				t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), j.Snapshot())
			}
			if got.State() != st || got.Version() != j.Version() {
				t.Fatalf("state=%s version=%d", got.State(), got.Version())
			}
		})
	}
}

func TestRestoreKeepsUnappliedScansAndTheException(t *testing.T) {
	j := inState(t, StateInTransit)
	_, err := j.RecordScan(scan("old", ScanStationArrival, 0), at(9))
	if !errors.Is(err, ErrScanOutOfOrder) {
		t.Fatal(err)
	}
	_, _ = j.RecordScan(scan("noedge", ScanDelivered, 20), at(21))
	_, err = j.RaiseException(at(30))
	mustNoErr(t, err)
	got, err := Restore(j.Snapshot())
	mustNoErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), j.Snapshot()) || got.LastExceptionAt() != at(30) {
		t.Fatalf("unapplied scans or exception lost: %+v", got.Snapshot())
	}
}

func TestRestoreNormalisesToUTCAndCopiesScans(t *testing.T) {
	s := inState(t, StateHandedOver).Snapshot()
	zone := time.FixedZone("x", 5400)
	s.StartedAt, s.LastScanAt, s.HandedOverAt = s.StartedAt.In(zone), s.LastScanAt.In(zone), s.HandedOverAt.In(zone)
	s.Scans[0].At = s.Scans[0].At.In(zone)
	j, err := Restore(s)
	mustNoErr(t, err)
	if j.StartedAt().Location() != time.UTC || j.LastScanAt().Location() != time.UTC || j.HandedOverAt().Location() != time.UTC ||
		j.Scans()[0].At.Location() != time.UTC {
		t.Fatal("restored instants must be UTC")
	}
	s.Scans[0].ID = "tampered"
	if j.Scans()[0].ID == "tampered" {
		t.Fatal("Restore must copy the scans")
	}
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	cases := []struct {
		name  string
		state State
		mut   func(*Snapshot)
	}{
		{"bad tracking", StateLabelled, func(s *Snapshot) { s.TrackingNumber = "" }},
		{"bad tier", StateLabelled, func(s *Snapshot) { s.Tier = "FAST" }},
		{"bad state", StateLabelled, func(s *Snapshot) { s.State = "DONE" }},
		{"no startedAt", StateLabelled, func(s *Snapshot) { s.StartedAt = time.Time{} }},
		{"version zero", StateLabelled, func(s *Snapshot) { s.Version = 0 }},
		{"version below scans", StateInTransit, func(s *Snapshot) { s.Version-- }},
		{"scan bad id", StateInTransit, func(s *Snapshot) { s.Scans[0].ID = "" }},
		{"scan bad node", StateInTransit, func(s *Snapshot) { s.Scans[0].NodeID = "" }},
		{"scan bad type", StateInTransit, func(s *Snapshot) { s.Scans[0].Type = "TELEPORT" }},
		{"scan no time", StateInTransit, func(s *Snapshot) { s.Scans[0].At = time.Time{} }},
		{"duplicate scan id", StateInTransit, func(s *Snapshot) { s.Scans[1].ID = s.Scans[0].ID }},
		{"applied scans out of order", StateInTransit, func(s *Snapshot) { s.Scans[1].At = s.Scans[0].At.Add(-time.Second) }},
		{"applied scan without edge", StateInTransit, func(s *Snapshot) { s.Scans[1].Type = ScanDelivered }},
		{"lastScanAt mismatch", StateInTransit, func(s *Snapshot) { s.LastScanAt = s.LastScanAt.Add(time.Second) }},
		{"lastNode mismatch", StateInTransit, func(s *Snapshot) { s.LastNodeID = "elsewhere" }},
		{"lastScanAt without scans", StateLabelled, func(s *Snapshot) { s.LastScanAt = at(1) }},
		{"handedOverAt mismatch", StateInTransit, func(s *Snapshot) { s.HandedOverAt = s.HandedOverAt.Add(time.Second) }},
		{"handedOverAt missing", StateInTransit, func(s *Snapshot) { s.HandedOverAt = time.Time{} }},
		{"deliveredAt on open", StateInTransit, func(s *Snapshot) { s.DeliveredAt = at(9) }},
		{"deliveredAt missing", StateDelivered, func(s *Snapshot) { s.DeliveredAt = time.Time{} }},
		{"state not reachable", StateInTransit, func(s *Snapshot) { s.State = StateAtStation }},
		{"state behind scans", StateAtStation, func(s *Snapshot) { s.State = StateInTransit }},
		{"returned from wrong state", StateInTransit, func(s *Snapshot) { s.State, s.ClosedAt = StateReturned, at(9) }},
		{"lost from wrong state", StateAtStation, func(s *Snapshot) { s.State, s.ClosedAt = StateLost, at(9) }},
		{"voided after handover", StateHandedOver, func(s *Snapshot) { s.State, s.ClosedAt = StateVoided, at(9) }},
		{"terminal without closedAt", StateDelivered, func(s *Snapshot) { s.ClosedAt = time.Time{} }},
		{"open with closedAt", StateInTransit, func(s *Snapshot) { s.ClosedAt = at(9) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := inState(t, c.state).Snapshot()
			c.mut(&s)
			if j, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) || j != nil {
				t.Fatalf("want ErrInvalidSnapshot, got %v %v", j, err)
			}
		})
	}
}

func TestRestoreBoundaries(t *testing.T) {
	s := inState(t, StateInTransit).Snapshot() // two scans: version 3 exactly
	if s.Version != int64(len(s.Scans))+1 {
		t.Fatalf("fixture: version %d for %d scans", s.Version, len(s.Scans))
	}
	if _, err := Restore(s); err != nil {
		t.Fatalf("version exactly 1 + scans must restore: %v", err)
	}
	s.Version++
	if _, err := Restore(s); err != nil {
		t.Fatalf("a higher version (unapplied scans, exceptions) must restore: %v", err)
	}
	// Equal instants between applied scans are in order.
	e := inState(t, StateInTransit).Snapshot()
	e.Scans[1].At, e.LastScanAt = e.Scans[0].At, e.Scans[0].At
	if _, err := Restore(e); err != nil {
		t.Fatalf("equal instants must restore: %v", err)
	}
	// Scan ids at the bound.
	b := inState(t, StateHandedOver).Snapshot()
	b.Scans[0].ID = strings.Repeat("a", shared.MaxScanID)
	if _, err := Restore(b); err != nil {
		t.Fatalf("a scan id of MaxScanID characters must restore: %v", err)
	}
	b.Scans[0].ID += "a"
	if _, err := Restore(b); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a longer scan id must not restore, got %v", err)
	}
}
