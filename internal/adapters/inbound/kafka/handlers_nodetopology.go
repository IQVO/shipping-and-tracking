package kafka

import (
	"context"
	"fmt"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// --- node-topology ---------------------------------------------------------

// serviceAreaAssignedData is the tolerant local copy of node-topology's
// ServiceAreaAssigned (ADR 0015). deliveryStationNodeId, regionId and every
// unknown field are read and dropped: this context keeps only the delivery
// mode, the declared carrier options and the assignment version. An absent
// deliveryMode or carriers list is the pre-C4 meaning: NETWORK_ROUTE, no
// options.
type serviceAreaAssignedData struct {
	PostalPrefix      string              `json:"postalPrefix"`
	DeliveryMode      string              `json:"deliveryMode"`
	Carriers          []carrierOptionData `json:"carriers"`
	AssignmentVersion int64               `json:"assignmentVersion"`
}

type carrierOptionData struct {
	CarrierID           string `json:"carrierId"`
	HandoffNodeID       string `json:"handoffNodeId"`
	CostPerPackageCents int64  `json:"costPerPackageCents"`
	TransitMinutes      int    `json:"transitMinutes"`
}

// areaModeOf validates the payload and maps it onto the read-model row.
func areaModeOf(d serviceAreaAssignedData) (repository.AreaMode, error) {
	if d.PostalPrefix == "" {
		return repository.AreaMode{}, fmt.Errorf("%w: postalPrefix is blank", errBadPayload)
	}
	if d.AssignmentVersion < 1 {
		return repository.AreaMode{}, fmt.Errorf("%w: assignmentVersion must be at least 1", errBadPayload)
	}
	mode := repository.DeliveryModeNetworkRoute
	if d.DeliveryMode == string(repository.DeliveryModeCarrierHandoff) {
		mode = repository.DeliveryModeCarrierHandoff
	} else if d.DeliveryMode != "" && d.DeliveryMode != string(repository.DeliveryModeNetworkRoute) {
		return repository.AreaMode{}, fmt.Errorf("%w: deliveryMode %q is not NETWORK_ROUTE or CARRIER_HANDOFF", errBadPayload, d.DeliveryMode)
	}
	carriers := make([]journey.CarrierOption, 0, len(d.Carriers))
	for _, c := range d.Carriers {
		if c.CarrierID == "" || c.HandoffNodeID == "" || c.TransitMinutes < 1 {
			return repository.AreaMode{}, fmt.Errorf("%w: a carrier option needs a carrierId, a handoffNodeId and a positive transitMinutes", errBadPayload)
		}
		carriers = append(carriers, journey.CarrierOption{CarrierID: c.CarrierID, HandoffNodeID: c.HandoffNodeID,
			CostPerPackageCents: c.CostPerPackageCents, TransitMinutes: c.TransitMinutes})
	}
	if mode == repository.DeliveryModeCarrierHandoff && len(carriers) == 0 {
		return repository.AreaMode{}, fmt.Errorf("%w: a CARRIER_HANDOFF area declares at least one carrier option", errBadPayload)
	}
	return repository.AreaMode{PostalPrefix: d.PostalPrefix, DeliveryMode: mode, Carriers: carriers, Version: d.AssignmentVersion}, nil
}

// AreaApplier is the ApplyAreaAssignment use case (ADR 0015).
type AreaApplier interface {
	Execute(ctx context.Context, cmd usecases.ApplyAreaCmd) (usecases.ApplyAreaResult, error)
}

// PointNoter is the NotePickupPoint use case (ADR 0016).
type PointNoter interface {
	Execute(ctx context.Context, cmd usecases.NotePointCmd) error
}

// nodeRegisteredData is the tolerant local copy of node-topology's
// NodeRegistered; only the pickup fields are read (ADR 0016).
type nodeRegisteredData struct {
	NodeID               string  `json:"nodeId"`
	NodeType             string  `json:"nodeType"`
	ServingStationNodeID *string `json:"servingStationNodeId"`
	PostalCode           string  `json:"postalCode"`
}

// nodeStateChangedData is the tolerant local copy of the node state events.
type nodeStateChangedData struct {
	NodeID string `json:"nodeId"`
}

// nodeStateTypes maps the node lifecycle events to the view's state column.
var nodeStateTypes = map[string]string{
	"com.retailnetwork.logistics.node-topology.node.NodeActivated": "ACTIVE",
	"com.retailnetwork.logistics.node-topology.node.NodeSuspended": "SUSPENDED",
	"com.retailnetwork.logistics.node-topology.node.NodeRetired":   "RETIRED",
}

// pickupPointOf maps a registered node onto a read-model row; a non-pickup
// node answers the zero row (a no-op in the use case).
func pickupPointOf(d nodeRegisteredData) repository.PickupPoint {
	if d.NodeType != "PICKUP_POINT" {
		return repository.PickupPoint{}
	}
	station := ""
	if d.ServingStationNodeID != nil {
		station = *d.ServingStationNodeID
	}
	return repository.PickupPoint{ID: d.NodeID, StationNodeID: station, PostalPrefix: d.PostalCode, State: "PROPOSED"}
}

// pointHandlers notes pickup points from the node lifecycle events.
func pointHandlers(uc PointNoter) map[string]HandlerFunc {
	out := map[string]HandlerFunc{
		"com.retailnetwork.logistics.node-topology.node.NodeRegistered": func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[nodeRegisteredData](e)
			if err != nil {
				return err
			}
			return uc.Execute(ctx, usecases.NotePointCmd{Consumer: ConsumerNodeTopology, EventID: e.ID(), Point: pickupPointOf(d)})
		},
	}
	for typ, state := range nodeStateTypes {
		state := state
		out[typ] = func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[nodeStateChangedData](e)
			if err != nil {
				return err
			}
			return uc.Execute(ctx, usecases.NotePointCmd{Consumer: ConsumerNodeTopology, EventID: e.ID(),
				Point: repository.PickupPoint{ID: d.NodeID, State: state}, Retired: state == "RETIRED"})
		}
	}
	return out
}

// NodeTopologyHandlers maps ServiceAreaAssigned to the AreaModeView upsert
// under the assignment-version guard, and the node lifecycle events to the
// PickupPointView (ADR 0016).
func NodeTopologyHandlers(uc AreaApplier, logger *slog.Logger, noter PointNoter) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	out := map[string]HandlerFunc{
		TypeServiceAreaAssigned: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[serviceAreaAssignedData](e)
			if err != nil {
				return err
			}
			area, err := areaModeOf(d)
			if err != nil {
				return err
			}
			res, err := uc.Execute(ctx, usecases.ApplyAreaCmd{Consumer: ConsumerNodeTopology, EventID: e.ID(), Area: area})
			if err != nil {
				return err
			}
			logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "skipped", res.Skipped,
				"stale", res.Stale, "stored", res.Stored)
			return nil
		},
	}
	if noter != nil {
		for typ, h := range pointHandlers(noter) {
			out[typ] = h
		}
	}
	return out
}

// NewNodeTopologyConsumer reads node-topology's topic under groupID
// (AREAS_CONSUMER_GROUP, never a literal).
func NewNodeTopologyConsumer(brokers []string, groupID string, uc AreaApplier, noter PointNoter, logger *slog.Logger) *Consumer {
	return newConsumer("node-topology consumer", brokers, NodeTopologyTopic, groupID, NodeTopologyHandlers(uc, logger, noter), logger)
}
