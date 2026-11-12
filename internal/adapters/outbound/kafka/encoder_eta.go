package kafka

import "github.com/claudioed/shipping-and-tracking/internal/domain/journey"

// packageEtaWindowAssignedData is the payload of PackageEtaWindowAssigned
// (ADR 0013). Every field is required; timestamps are RFC 3339 UTC.
type packageEtaWindowAssignedData struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId"`
	OrderID        string `json:"orderId"`
	EtaStart       string `json:"etaStart"`
	EtaEnd         string `json:"etaEnd"`
	AssignedAt     string `json:"assignedAt"`
}

func etaPayload(e journey.PackageEtaWindowAssigned) packageEtaWindowAssignedData {
	return packageEtaWindowAssignedData{
		TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID,
		EtaStart: ts(e.EtaStart), EtaEnd: ts(e.EtaEnd), AssignedAt: ts(e.AssignedAt),
	}
}
