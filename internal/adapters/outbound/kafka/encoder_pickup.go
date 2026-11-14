package kafka

import (
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// readyForCollectionData is the wire payload of PackageReadyForCollection
// (ADR 0016): the wave deposited the package at its point; holdUntil is the
// retention deadline the sweep returns the package after.
type readyForCollectionData struct {
	TrackingNumber       string `json:"trackingNumber"`
	AssignmentID         string `json:"assignmentId"`
	OrderID              string `json:"orderId"`
	PickupPointID        string `json:"pickupPointId"`
	ReadyForCollectionAt string `json:"readyForCollectionAt"`
	HoldUntil            string `json:"holdUntil"`
}

func readyForCollectionPayload(e journey.PackageReadyForCollection) readyForCollectionData {
	return readyForCollectionData{
		TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID,
		PickupPointID: e.PickupPointID, ReadyForCollectionAt: ts(e.ReadyForCollectionAt), HoldUntil: ts(e.HoldUntil),
	}
}

// collectedData is the wire payload of PackageCollected (ADR 0016). ProofCode
// is omitted when no proof was taken.
type collectedData struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId"`
	OrderID        string `json:"orderId"`
	PickupPointID  string `json:"pickupPointId"`
	CollectedAt    string `json:"collectedAt"`
	ProofCode      string `json:"proofCode,omitempty"`
}

func collectedPayload(e journey.PackageCollected) collectedData {
	return collectedData{
		TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID,
		PickupPointID: e.PickupPointID, CollectedAt: ts(e.CollectedAt), ProofCode: e.ProofCode,
	}
}
