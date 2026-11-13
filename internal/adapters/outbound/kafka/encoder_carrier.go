package kafka

import (
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// packageHandedToCarrierData is the wire payload of journey.PackageHandedToCarrier
// (ADR 0015). No optional fields: the event is raised once per journey.
type packageHandedToCarrierData struct {
	TrackingNumber          string `json:"trackingNumber"`
	AssignmentID            string `json:"assignmentId"`
	OrderID                 string `json:"orderId"`
	CarrierID               string `json:"carrierId"`
	HandoffNodeID           string `json:"handoffNodeId"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
	HandedAt                string `json:"handedAt"`
}

func carrierHandoffPayload(e journey.PackageHandedToCarrier) packageHandedToCarrierData {
	return packageHandedToCarrierData{
		TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID,
		CarrierID: e.CarrierID, HandoffNodeID: e.HandoffNodeID,
		DestinationPostalPrefix: e.DestinationPostalPrefix, HandedAt: ts(e.HandedAt),
	}
}
