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

func (j *Journey) closedErr() error {
	return fmt.Errorf("%w: journey %s is %s", ErrJourneyClosed, j.tracking, j.state)
}

// RaiseException records a NO_SCAN exception in any open state; the state does
// not change (RaiseException).
func (j *Journey) RaiseException(now time.Time) ([]shared.Event, error) {
	if j.state.Terminal() {
		return nil, j.closedErr()
	}
	j.lastExceptedAt = shared.UTC(now)
	j.version++
	return []shared.Event{PackageExceptionRaised{
		Header: shared.Header{Version: j.version, At: j.lastExceptedAt}, TrackingNumber: j.tracking,
		AssignmentID: j.assignmentID, OrderID: j.orderID, Reason: ReasonNoScan, RaisedAt: j.lastExceptedAt,
	}}, nil
}

// ReturnToSender moves an AT_STATION journey to RETURNED (ReturnToSender).
func (j *Journey) ReturnToSender(returnedAt, now time.Time) ([]shared.Event, error) {
	if err := j.closeFrom(StateAtStation, StateReturned, now); err != nil {
		return nil, err
	}
	return []shared.Event{PackageReturned{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		OrderID: j.orderID, ReturnedAt: shared.UTC(returnedAt),
	}}, nil
}

// DeclareLost moves an IN_TRANSIT journey to LOST (DeclareLost).
func (j *Journey) DeclareLost(declaredAt, now time.Time) ([]shared.Event, error) {
	if err := j.closeFrom(StateInTransit, StateLost, now); err != nil {
		return nil, err
	}
	return []shared.Event{PackageLost{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		OrderID: j.orderID, DeclaredAt: shared.UTC(declaredAt),
	}}, nil
}

func (j *Journey) closeFrom(from, to State, now time.Time) error {
	if j.state.Terminal() {
		return j.closedErr()
	}
	if j.state != from {
		return fmt.Errorf("%w: journey %s is %s, not %s", shared.ErrIllegalTransition, j.tracking, j.state, from)
	}
	j.state = to
	j.closedAt = shared.UTC(now)
	j.version++
	return nil
}

// Void closes a LABELLED journey as VOIDED (ADR 0002 A13). It raises no event:
// LabelVoided announces the void.
func (j *Journey) Void(now time.Time) error {
	if j.state.Terminal() {
		return j.closedErr()
	}
	if j.state != StateLabelled {
		return fmt.Errorf("%w: journey %s is %s", ErrVoidAfterHandover, j.tracking, j.state)
	}
	j.state = StateVoided
	j.closedAt = shared.UTC(now)
	j.version++
	return nil
}

// unanswered reports whether a NO_SCAN exception was raised and no applied scan
// arrived since.
func (j *Journey) unanswered() bool {
	return !j.lastExceptedAt.IsZero() && !j.lastExceptedAt.Before(j.lastScanAt)
}

// Silent reports whether ExceptionOnSilence must raise NO_SCAN now: a watched
// state, a window with no applied scan, and no exception already raised for
// this silence (ADR 0002 A7).
func (j *Journey) Silent(now time.Time) bool {
	w := SilenceWindow(j.tier, j.state)
	if w == 0 || j.lastScanAt.IsZero() || j.unanswered() {
		return false
	}
	return !now.Before(j.lastScanAt.Add(w))
}

// Lost reports whether the package must be declared lost now: IN_TRANSIT with
// a NO_SCAN exception that stayed unanswered for LostAfterException (ADR 0002 A8).
func (j *Journey) Lost(now time.Time) bool {
	return j.state == StateInTransit && j.unanswered() && !now.Before(j.lastExceptedAt.Add(LostAfterException()))
}

// TrackingNumber returns the root id.
func (j *Journey) TrackingNumber() TrackingNumber { return j.tracking }

// LabelRef returns the label of the package.
func (j *Journey) LabelRef() string { return j.labelRef }

// PONumber returns the purchase order number.
func (j *Journey) PONumber() string { return j.poNumber }

// AssignmentID returns the fulfillment assignment id.
func (j *Journey) AssignmentID() string { return j.assignmentID }

// OrderID returns the customer order id.
func (j *Journey) OrderID() string { return j.orderID }

// Tier returns the service tier.
func (j *Journey) Tier() shared.Tier { return j.tier }

// State returns the current state.
func (j *Journey) State() State { return j.state }

// Scans returns a copy of the stored scans in the order they were recorded.
func (j *Journey) Scans() []Scan { return append([]Scan(nil), j.scans...) }

// StartedAt returns when the journey was started.
func (j *Journey) StartedAt() time.Time { return j.startedAt }

// LastScanAt returns the instant of the last applied scan (zero before any).
func (j *Journey) LastScanAt() time.Time { return j.lastScanAt }

// LastNodeID returns the node of the last applied scan (empty before any).
func (j *Journey) LastNodeID() string { return j.lastNodeID }

// HandedOverAt returns the handover instant (zero before handover).
func (j *Journey) HandedOverAt() time.Time { return j.handedOverAt }

// DeliveredAt returns the delivery instant (zero unless DELIVERED).
func (j *Journey) DeliveredAt() time.Time { return j.deliveredAt }

// ClosedAt returns when the journey reached a terminal state (zero while open).
func (j *Journey) ClosedAt() time.Time { return j.closedAt }

// LastExceptionAt returns when the last exception was raised (zero if none).
func (j *Journey) LastExceptionAt() time.Time { return j.lastExceptedAt }

// Version returns the aggregate version.
func (j *Journey) Version() int64 { return j.version }

// Snapshot returns the persisted form.
func (j *Journey) Snapshot() Snapshot {
	return Snapshot{
		TrackingNumber: j.tracking, LabelRef: j.labelRef, PONumber: j.poNumber, AssignmentID: j.assignmentID,
		OrderID: j.orderID, Tier: j.tier, State: j.state, Scans: j.Scans(), StartedAt: j.startedAt,
		LastScanAt: j.lastScanAt, LastNodeID: j.lastNodeID, HandedOverAt: j.handedOverAt, DeliveredAt: j.deliveredAt,
		ClosedAt: j.closedAt, LastExceptionAt: j.lastExceptedAt, Version: j.version,
	}
}
