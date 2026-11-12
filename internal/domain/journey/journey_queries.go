package journey

import (
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

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
		ClosedAt: j.closedAt, LastExceptionAt: j.lastExceptedAt, EtaStart: j.etaStart, EtaEnd: j.etaEnd,
		EtaAssignedAt: j.etaAssignedAt, Version: j.version,
	}
}
