package journey

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Restore rebuilds a persisted journey, validating every invariant. The
// applied scans are replayed through the edge table, so a snapshot whose state
// cannot be reached by its own scans is refused.
func Restore(s Snapshot) (*Journey, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("journey %q: %v", s.TrackingNumber, err)
	}
	scans := make([]Scan, len(s.Scans))
	for i, sc := range s.Scans {
		sc.At = shared.UTC(sc.At)
		scans[i] = sc
	}
	return &Journey{
		tracking: s.TrackingNumber, labelRef: s.LabelRef, poNumber: s.PONumber, assignmentID: s.AssignmentID,
		orderID: s.OrderID, tier: s.Tier, state: s.State, scans: scans, startedAt: shared.UTC(s.StartedAt),
		lastScanAt: shared.UTC(s.LastScanAt), lastNodeID: s.LastNodeID, handedOverAt: shared.UTC(s.HandedOverAt),
		deliveredAt: shared.UTC(s.DeliveredAt), closedAt: shared.UTC(s.ClosedAt),
		lastExceptedAt: shared.UTC(s.LastExceptionAt), version: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if err := checkIdentity(s.TrackingNumber, s.LabelRef, s.PONumber, s.AssignmentID, s.OrderID, s.Tier); err != nil {
		return err
	}
	if !s.State.Valid() {
		return fmt.Errorf("state %q is not a journey state", s.State)
	}
	if s.StartedAt.IsZero() || s.Version < int64(len(s.Scans))+1 {
		return errors.New("startedAt and a version of at least 1 + the number of scans are required")
	}
	if err := checkScans(s.Scans); err != nil {
		return err
	}
	end, err := replay(s)
	if err != nil {
		return err
	}
	return checkClosing(s, end)
}

func checkScans(scans []Scan) error {
	seen := make(map[string]bool, len(scans))
	for _, sc := range scans {
		in := ScanInput{ID: sc.ID, NodeID: sc.NodeID, Type: sc.Type, At: sc.At}
		if err := in.validate(); err != nil {
			return err
		}
		if seen[sc.ID] {
			return fmt.Errorf("scanId %q appears twice", sc.ID)
		}
		seen[sc.ID] = true
	}
	return nil
}

// replay walks the applied scans from LABELLED and checks the cached fields
// that derive from them. It returns the state the scans lead to.
func replay(s Snapshot) (State, error) {
	state := StateLabelled
	var last, handed, delivered time.Time
	var lastNode string
	for _, sc := range s.Scans {
		if !sc.Applied {
			continue
		}
		if sc.At.Before(last) {
			return "", fmt.Errorf("applied scan %q is older than the previous applied scan", sc.ID)
		}
		to, ok := next(sc.Type, state)
		if !ok {
			return "", fmt.Errorf("applied scan %q (%s) has no edge from %s", sc.ID, sc.Type, state)
		}
		if state == StateLabelled && to == StateHandedOver {
			handed = sc.At
		}
		if to == StateDelivered {
			delivered = sc.At
		}
		state, last, lastNode = to, sc.At, sc.NodeID
	}
	if !s.LastScanAt.Equal(last) {
		return "", errors.New("lastScanAt must be the instant of the last applied scan")
	}
	if s.LastNodeID != lastNode {
		return "", errors.New("lastNodeId must be the node of the last applied scan")
	}
	if !s.HandedOverAt.Equal(handed) {
		return "", errors.New("handedOverAt must be the instant of the pickup departure scan")
	}
	if !s.DeliveredAt.Equal(delivered) {
		return "", errors.New("deliveredAt must be the instant of the delivered scan")
	}
	return state, nil
}

// closing maps a state a command (not a scan) can set to the state the applied
// scans must have led to.
var closing = map[State]State{StateReturned: StateAtStation, StateLost: StateInTransit, StateVoided: StateLabelled}

func checkClosing(s Snapshot, end State) error {
	want := s.State
	if from, ok := closing[s.State]; ok {
		want = from
	}
	if end != want {
		return fmt.Errorf("state %s is not reachable by the applied scans (they end in %s)", s.State, end)
	}
	if s.State.Terminal() != !s.ClosedAt.IsZero() {
		return errors.New("closedAt is set exactly when the state is terminal")
	}
	return nil
}
