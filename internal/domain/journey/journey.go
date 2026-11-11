// Package journey is the PackageJourney aggregate: the customer-visible
// timeline of one package, identified by its tracking number. It owns its Scan
// entities and folds facts from the vendor, linehaul and last-mile into one
// forward-only state that ends in DELIVERED, RETURNED, LOST or VOIDED.
//
// States: LABELLED, HANDED_OVER, IN_TRANSIT, AT_STATION, OUT_FOR_DELIVERY,
// DELIVERED, RETURNED, LOST and VOIDED (ADR 0002 A13). Invariants: scans apply
// in time order, an older one is stored but does not move the state
// (ErrScanOutOfOrder); a scanId is applied once (ErrDuplicateScan); the
// terminal states are closed (ErrJourneyClosed).
package journey

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// The PackageJourney invariants of the model, by the model's names.
var (
	// ErrScanOutOfOrder: an older scan is stored but does not move the state (409).
	ErrScanOutOfOrder = errors.New("scan out of order")
	// ErrDuplicateScan: a scanId is applied once (409). Nothing is stored.
	ErrDuplicateScan = errors.New("duplicate scan")
	// ErrJourneyClosed: DELIVERED, RETURNED, LOST and VOIDED are terminal (409).
	ErrJourneyClosed = errors.New("journey closed")
	// ErrVoidAfterHandover: a label cannot be voided once the package left the
	// vendor (ADR 0002 A13, 409).
	ErrVoidAfterHandover = errors.New("void after handover")
)

// Scan is one observation of a package at a node. Applied is false for a scan
// that was stored but did not move the state.
type Scan struct {
	ID      string
	NodeID  string
	Type    ScanType
	At      time.Time
	Applied bool
}

// Journey is the aggregate root.
type Journey struct {
	tracking       TrackingNumber
	labelRef       string
	poNumber       string
	assignmentID   string
	orderID        string
	tier           shared.Tier
	state          State
	scans          []Scan
	startedAt      time.Time
	lastScanAt     time.Time
	lastNodeID     string
	handedOverAt   time.Time
	deliveredAt    time.Time
	closedAt       time.Time
	lastExceptedAt time.Time
	version        int64
}

// Snapshot is the persisted form of a journey. Restore validates it.
type Snapshot struct {
	TrackingNumber  TrackingNumber
	LabelRef        string
	PONumber        string
	AssignmentID    string
	OrderID         string
	Tier            shared.Tier
	State           State
	Scans           []Scan
	StartedAt       time.Time
	LastScanAt      time.Time
	LastNodeID      string
	HandedOverAt    time.Time
	DeliveredAt     time.Time
	ClosedAt        time.Time
	LastExceptionAt time.Time
	Version         int64
}

// NewJourney is the input of Start.
type NewJourney struct {
	TrackingNumber TrackingNumber
	LabelRef       string
	PONumber       string
	AssignmentID   string
	OrderID        string
	Tier           shared.Tier
}

func checkIdentity(tracking TrackingNumber, labelRef, po, assignment, order string, tier shared.Tier) error {
	if !tracking.Valid() {
		return shared.Invalid("trackingNumber %q is not a valid id", tracking)
	}
	for _, f := range [][2]string{{"labelRef", labelRef}, {"poNumber", po}, {"assignmentId", assignment}, {"orderId", order}} {
		if !shared.ValidID(f[1]) {
			return shared.Invalid("%s %q is not a valid id", f[0], f[1])
		}
	}
	if !tier.Valid() {
		return shared.Invalid("tier %q is not SAME_DAY, NEXT_DAY, TWO_DAY or STANDARD", tier)
	}
	return nil
}

// Start creates a LABELLED journey at version 1 (StartJourney). It raises no
// event: LabelIssued announces the package (ADR 0002 A4).
func Start(in NewJourney, now time.Time) (*Journey, error) {
	if err := checkIdentity(in.TrackingNumber, in.LabelRef, in.PONumber, in.AssignmentID, in.OrderID, in.Tier); err != nil {
		return nil, err
	}
	return &Journey{
		tracking: in.TrackingNumber, labelRef: in.LabelRef, poNumber: in.PONumber, assignmentID: in.AssignmentID,
		orderID: in.OrderID, tier: in.Tier, state: StateLabelled, startedAt: shared.UTC(now), version: 1,
	}, nil
}

// ScanInput is the input of RecordScan.
type ScanInput struct {
	ID     string
	NodeID string
	Type   ScanType
	At     time.Time
}

// Result tells the caller what a command did. Changed is true when the
// aggregate must be saved even though the command returned an error: a scan
// stored out of order, or without an edge from the state it met, is kept
// (ErrScanOutOfOrder, ErrIllegalTransition).
type Result struct {
	Events  []shared.Event
	Changed bool
}

func (in ScanInput) validate() error {
	if !shared.ValidIDN(in.ID, shared.MaxScanID) {
		return shared.Invalid("scanId %q is not a valid id of at most %d characters", in.ID, shared.MaxScanID)
	}
	if !shared.ValidID(in.NodeID) {
		return shared.Invalid("nodeId %q is not a valid id", in.NodeID)
	}
	if !in.Type.Valid() {
		return shared.Invalid("scanType %q is not a known scan type", in.Type)
	}
	if in.At.IsZero() {
		return shared.Invalid("at is required")
	}
	return nil
}

func (j *Journey) hasScan(id string) bool {
	for _, s := range j.scans {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (j *Journey) store(in ScanInput, applied bool) {
	j.scans = append(j.scans, Scan{ID: in.ID, NodeID: in.NodeID, Type: in.Type, At: shared.UTC(in.At), Applied: applied})
	j.version++
}

// RecordScan applies one scan. Order of checks: validation, ErrJourneyClosed,
// ErrDuplicateScan, ErrScanOutOfOrder, then the edge table (ErrIllegalTransition).
// The last two keep the scan with Applied=false and report Result.Changed.
func (j *Journey) RecordScan(in ScanInput, now time.Time) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}
	if j.state.Terminal() {
		return Result{}, fmt.Errorf("%w: journey %s is %s", ErrJourneyClosed, j.tracking, j.state)
	}
	if j.hasScan(in.ID) {
		return Result{}, fmt.Errorf("%w: %s", ErrDuplicateScan, in.ID)
	}
	if !j.lastScanAt.IsZero() && in.At.Before(j.lastScanAt) {
		j.store(in, false)
		return Result{Changed: true}, fmt.Errorf("%w: %s is older than the last applied scan at %s",
			ErrScanOutOfOrder, in.ID, j.lastScanAt.Format(time.RFC3339))
	}
	to, ok := next(in.Type, j.state)
	if !ok {
		j.store(in, false)
		return Result{Changed: true}, fmt.Errorf("%w: a %s scan has no edge from %s", shared.ErrIllegalTransition, in.Type, j.state)
	}
	return Result{Events: j.apply(in, to, now), Changed: true}, nil
}

// apply moves the journey along an edge and returns the events of the scan.
func (j *Journey) apply(in ScanInput, to State, now time.Time) []shared.Event {
	from := j.state
	j.store(in, true)
	j.state = to
	j.lastScanAt = shared.UTC(in.At)
	j.lastNodeID = in.NodeID
	h := shared.Header{Version: j.version, At: shared.UTC(now)}
	evs := []shared.Event{PackageScanned{
		Header: h, TrackingNumber: j.tracking, ScanID: in.ID, NodeID: in.NodeID, ScanType: in.Type,
		ScannedAt: j.lastScanAt, State: to,
	}}
	if from == StateLabelled && to == StateHandedOver {
		j.handedOverAt = j.lastScanAt
		evs = append(evs, PackageHandedOver{
			Header: h, TrackingNumber: j.tracking, PONumber: j.poNumber, NodeID: in.NodeID, HandedOverAt: j.handedOverAt,
		})
	}
	if to == StateDelivered {
		j.deliveredAt = j.lastScanAt
		j.closedAt = shared.UTC(now)
		evs = append(evs, PackageDelivered{
			Header: h, TrackingNumber: j.tracking, AssignmentID: j.assignmentID, OrderID: j.orderID, DeliveredAt: j.deliveredAt,
		})
	}
	return evs
}
