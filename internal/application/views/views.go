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
	PONumber       string     `json:"poNumber"`
	PackageNo      int        `json:"packageNo"`
	AssignmentID   string     `json:"assignmentId"`
	OrderID        string     `json:"orderId"`
	Tier           string     `json:"tier"`
	Status         string     `json:"status"`
	IssuedAt       time.Time  `json:"issuedAt"`
	VoidReason     string     `json:"voidReason,omitempty"`
	VoidedAt       *time.Time `json:"voidedAt,omitempty"`
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
	Scans          []Scan     `json:"scans"`
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
	return v
}
