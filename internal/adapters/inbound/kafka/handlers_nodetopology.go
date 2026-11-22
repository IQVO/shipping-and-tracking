package kafka

import (
	"context"
	"fmt"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// --- node-topology ---------------------------------------------------------

// serviceAreaAssignedData is the tolerant local copy of node-topology's
// ServiceAreaAssigned (ADR 0015). regionId and every unknown field are read
// and dropped: this context keeps the delivery mode, the declared carrier
// options, the assignment version and, since ADR 0019, the delivery station
// node id (it is published on LabelIssued). An absent deliveryMode or carriers
// list is the pre-C4 meaning: NETWORK_ROUTE, no options.
type serviceAreaAssignedData struct {
	PostalPrefix          string              `json:"postalPrefix"`
	DeliveryStationNodeID string              `json:"deliveryStationNodeId"`
	DeliveryMode          string              `json:"deliveryMode"`
	Carriers              []carrierOptionData `json:"carriers"`
	AssignmentVersion     int64               `json:"assignmentVersion"`
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
	return repository.AreaMode{PostalPrefix: d.PostalPrefix, DeliveryMode: mode, Carriers: carriers, Version: d.AssignmentVersion,
		DeliveryStationNodeID: stationOf(d.DeliveryStationNodeID)}, nil
}

// stationOf is the station id this context keeps: the node id as sent, or ""
// for an absent, blank or malformed one. A station is advisory (ADR 0019): a
// value no label could carry is treated as absent rather than refusing the
// whole assignment, whose mode and carriers are still wanted.
func stationOf(id string) string {
	if !shared.ValidID(id) {
		return ""
	}
	return id
}

// AreaApplier is the ApplyAreaAssignment use case (ADR 0015).
type AreaApplier interface {
	Execute(ctx context.Context, cmd usecases.ApplyAreaCmd) (usecases.ApplyAreaResult, error)
}

// PointNoter is the NotePickupPoint use case (ADR 0016): Execute for a
// registration, ExecuteStatus for a status fact.
type PointNoter interface {
	Execute(ctx context.Context, cmd usecases.NotePointCmd) error
	ExecuteStatus(ctx context.Context, cmd usecases.NotePointStatusCmd) error
}

// NodeNoter is the NoteNodeDestination use case (ADR 0018).
type NodeNoter interface {
	Execute(ctx context.Context, cmd usecases.NoteNodeDestinationCmd) error
}

// nodeRegisteredData is the tolerant local copy of node-topology's
// NodeRegistered. nodeId and postalCode are read for EVERY node type (the
// NodeDestinationView, ADR 0018); nodeType and servingStationNodeId only tell
// and place a pickup point (ADR 0016). Everything else is dropped.
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
	"com.retailnetwork.logistics.node-topology.node.NodeActivated": repository.PickupPointActive,
	"com.retailnetwork.logistics.node-topology.node.NodeSuspended": repository.PickupPointSuspended,
	"com.retailnetwork.logistics.node-topology.node.NodeRetired":   repository.PickupPointRetired,
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
	return repository.PickupPoint{ID: d.NodeID, StationNodeID: station, PostalPrefix: d.PostalCode, State: repository.PickupPointProposed}
}

// TypeNodeRegistered feeds the NodeDestinationView (every node type, ADR 0018)
// and the PickupPointView (pickup points, ADR 0016).
const TypeNodeRegistered = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"

// nodeRegisteredHandler folds one NodeRegistered into the two node views, each
// in its own unit and under its own inbox identity: the destination prefix of
// the node, whatever its type, then the pickup-point row. A redelivery after
// a failure between the two is harmless: the fold that committed skips its
// claimed id. A nil fold is not wired.
func nodeRegisteredHandler(points PointNoter, nodes NodeNoter) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[nodeRegisteredData](e)
		if err != nil {
			return err
		}
		if d.NodeID == "" {
			return fmt.Errorf("%w: nodeId is blank", errBadPayload)
		}
		if nodes != nil {
			if err := nodes.Execute(ctx, usecases.NoteNodeDestinationCmd{Consumer: ConsumerNodeDestinations, EventID: e.ID(),
				NodeID: d.NodeID, PostalCode: d.PostalCode}); err != nil {
				return err
			}
		}
		if points == nil {
			return nil
		}
		return points.Execute(ctx, usecases.NotePointCmd{Consumer: ConsumerNodeTopology, EventID: e.ID(), Point: pickupPointOf(d)})
	}
}

// nodeHandlers maps the node events: NodeRegistered to both node views, the
// three status facts to the PickupPointView only. The status facts carry a
// nodeId and an instant or a reason, nothing a prefix could come from, and
// the NodeDestinationView keeps no state: they never reach it.
func nodeHandlers(uc PointNoter, nodes NodeNoter) map[string]HandlerFunc {
	out := map[string]HandlerFunc{}
	if uc != nil || nodes != nil {
		out[TypeNodeRegistered] = nodeRegisteredHandler(uc, nodes)
	}
	if uc == nil {
		return out
	}
	for typ, state := range nodeStateTypes {
		state := state
		out[typ] = func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[nodeStateChangedData](e)
			if err != nil {
				return err
			}
			return uc.ExecuteStatus(ctx, usecases.NotePointStatusCmd{Consumer: ConsumerNodeTopology,
				EventID: e.ID(), NodeID: d.NodeID, State: state})
		}
	}
	return out
}

// NodeTopologyHandlers maps ServiceAreaAssigned to the AreaModeView upsert
// under the assignment-version guard, the node lifecycle events to the
// PickupPointView (ADR 0016) and NodeRegistered also to the
// NodeDestinationView (ADR 0018).
func NodeTopologyHandlers(uc AreaApplier, logger *slog.Logger, noter PointNoter, nodes NodeNoter) map[string]HandlerFunc {
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
			res, err := uc.Execute(ctx, usecases.ApplyAreaCmd{Consumer: ConsumerAreaModes, EventID: e.ID(), Area: area})
			if err != nil {
				return err
			}
			logger.InfoContext(ctx, "event processed", "type", sanitizeLog(e.Type()), "event_id", sanitizeLog(e.ID()), "skipped", res.Skipped,
				"stale", res.Stale, "stored", res.Stored)
			return nil
		},
	}
	for typ, h := range nodeHandlers(noter, nodes) {
		out[typ] = h
	}
	return out
}

// NewNodeTopologyConsumer reads node-topology's topic under groupID
// (AREAS_CONSUMER_GROUP, never a literal).
func NewNodeTopologyConsumer(brokers []string, groupID string, uc AreaApplier, noter PointNoter, nodes NodeNoter, logger *slog.Logger) *Consumer {
	return newConsumer("node-topology consumer", brokers, NodeTopologyTopic, groupID, NodeTopologyHandlers(uc, logger, noter, nodes), logger)
}
