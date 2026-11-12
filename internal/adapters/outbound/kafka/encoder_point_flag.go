package kafka

import "github.com/claudioed/shipping-and-tracking/internal/domain/journey"

// packageDeliveryPointFlaggedData is the payload of
// PackageDeliveryPointFlagged (ADR 0014). Every field is required; the reason
// is one of the two codes the producer publishes, never prose.
type packageDeliveryPointFlaggedData struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId"`
	OrderID        string `json:"orderId"`
	Reason         string `json:"reason"`
	FlaggedAt      string `json:"flaggedAt"`
}

func pointFlagPayload(e journey.PackageDeliveryPointFlagged) packageDeliveryPointFlaggedData {
	return packageDeliveryPointFlaggedData{
		TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID,
		Reason: e.Reason, FlaggedAt: ts(e.FlaggedAt),
	}
}
