// Package views holds the read DTOs the use cases return: one per schema of
// apis/openapi.yaml (Label, Journey, Scan). The JSON tags are the REST wire
// names, so the HTTP adapter marshals them as they are. A view is a copy;
// nothing in it aliases an aggregate. No view has an address field: the
// ship-to never leaves the encrypted snapshot (ADR 0002 A9).
package views

import (
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// Label is the label metadata.
type Label struct {
	LabelRef       string     `json:"labelRef"`
	TrackingNumber string     `json:"trackingNumber"`
	Carrier        string     `json:"carrier"`
	PONumber       string     `json:"poNumber,omitempty"`
	PackageNo      int        `json:"packageNo"`
	AssignmentID   string     `json:"assignmentId,omitempty"`
	OrderID        string     `json:"orderId"`
	Tier           string     `json:"tier"`
	Status         string     `json:"status"`
	IssuedAt       time.Time  `json:"issuedAt"`
	VoidReason     string     `json:"voidReason,omitempty"`
	VoidedAt       *time.Time `json:"voidedAt,omitempty"`
	// Purpose and ReturnAuthorizationID appear only on a reverse label; an
	// outbound label omits both (ADR 0017).
	Purpose               string `json:"purpose,omitempty"`
	ReturnAuthorizationID string `json:"returnAuthorizationId,omitempty"`
}

// LabelFrom copies a label into its view.
func LabelFrom(l *label.ShippingLabel) Label {
	v := Label{
		LabelRef: string(l.Ref()), TrackingNumber: string(l.TrackingNumber()), Carrier: l.Carrier(), PONumber: l.PONumber(),
		PackageNo: l.PackageNo(), AssignmentID: l.AssignmentID(), OrderID: l.OrderID(), Tier: string(l.Tier()),
		Status: string(l.Status()), IssuedAt: l.IssuedAt(), VoidReason: l.VoidReason(),
	}
	if !l.VoidedAt().IsZero() {
		at := l.VoidedAt()
		v.VoidedAt = &at
	}
	if l.Purpose() == label.PurposeReturn {
		v.Purpose, v.ReturnAuthorizationID = string(l.Purpose()), l.ReturnAuthorizationID()
	}
	return v
}

// Scan is one stored scan of a journey.
type Scan struct {
	ScanID   string    `json:"scanId"`
	NodeID   string    `json:"nodeId"`
	ScanType string    `json:"scanType"`
	At       time.Time `json:"at"`
	Applied  bool      `json:"applied"`
}

// Journey is the customer-visible timeline of one package.
type Journey struct {
	TrackingNumber string     `json:"trackingNumber"`
	State          string     `json:"state"`
	PONumber       string     `json:"poNumber"`
	AssignmentID   string     `json:"assignmentId"`
	OrderID        string     `json:"orderId"`
	Tier           string     `json:"tier"`
	HandedOverAt   *time.Time `json:"handedOverAt,omitempty"`
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
	// EtaStart and EtaEnd are the day-of ETA window (ADR 0013), present only while
	// the journey is OUT_FOR_DELIVERY and the window is not older than the last scan.
	EtaStart *time.Time `json:"etaStart,omitempty"`
	EtaEnd   *time.Time `json:"etaEnd,omitempty"`
	// The pickup pair of ADR 0016, present only on a pickup journey.
	DestinationKind      string     `json:"destinationKind,omitempty"`
	PickupPointID        string     `json:"pickupPointId,omitempty"`
	ReadyForCollectionAt *time.Time `json:"readyForCollectionAt,omitempty"`
	HoldUntil            *time.Time `json:"holdUntil,omitempty"`
	Scans                []Scan     `json:"scans"`
}

// JourneyFrom copies a journey into its view. Scans is never nil.
func JourneyFrom(j *journey.Journey) Journey {
	scans := j.Scans()
	v := Journey{
		TrackingNumber: string(j.TrackingNumber()), State: string(j.State()), PONumber: j.PONumber(),
		AssignmentID: j.AssignmentID(), OrderID: j.OrderID(), Tier: string(j.Tier()), Scans: make([]Scan, len(scans)),
	}
	for i, s := range scans {
		v.Scans[i] = Scan{ScanID: s.ID, NodeID: s.NodeID, ScanType: string(s.Type), At: s.At, Applied: s.Applied}
	}
	if t := j.HandedOverAt(); !t.IsZero() {
		v.HandedOverAt = &t
	}
	if t := j.DeliveredAt(); !t.IsZero() {
		v.DeliveredAt = &t
	}
	if j.EtaCurrent() {
		start, end := j.EtaStart(), j.EtaEnd()
		v.EtaStart, v.EtaEnd = &start, &end
	}
	if j.PickupPointID() != "" {
		v.DestinationKind, v.PickupPointID = "PICKUP_POINT", j.PickupPointID()
		if t := j.ReadyForCollectionAt(); !t.IsZero() {
			v.ReadyForCollectionAt = &t
		}
		if t := j.HoldUntil(); !t.IsZero() {
			v.HoldUntil = &t
		}
	}
	return v
}
