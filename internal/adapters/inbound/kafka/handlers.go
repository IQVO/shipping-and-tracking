package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Consumed topics: the producers' integration topics (their contracts are
// restated in apis/asyncapi.yaml and ADR 0003, never imported).
const (
	VendorTopic   = "retailnetwork.vendor-connectivity.events"
	LinehaulTopic = "retailnetwork.linehaul-transportation.events"
	LastMileTopic = "retailnetwork.last-mile-delivery.events"
	CatalogTopic  = "retailnetwork.offer-availability.events"
)

// Consumed CloudEvents types (byte-identical to the producers' catalogues).
const (
	TypeVendorShipmentConfirmed = "com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed"
	TypeLoadDeparted            = "com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted"
	TypeLoadArrived             = "com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived"
	TypePackageOutForDelivery   = "com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery"
	TypeDeliveryAttempted       = "com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted"
	// The entity segment of PackageReturnInitiated is last-mile-delivery's
	// choice (ADR 0003): the pushed AsyncAPI says packageattempthistory; the
	// route spelling is a tolerated alias.
	TypePackageReturnInitiated        = "com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated"
	TypePackageReturnInitiatedOnRoute = "com.retailnetwork.logistics.last-mile-delivery.route.PackageReturnInitiated"
	TypeCatalogItemCreated            = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated"
)

// Inbox consumer names (processed_events.consumer).
const (
	ConsumerVendor   = "vendor-connectivity"
	ConsumerLinehaul = "linehaul-transportation"
	ConsumerLastMile = "last-mile-delivery"
	ConsumerCatalog  = "offer-availability"
)

// Departure and arrival vocabulary of linehaul (ADR 0002 A20).
const (
	departureKindPickup = "PICKUP"
	nodeTypeStation     = "DELIVERY_STATION"
	outcomeDelivered    = "DELIVERED"
)

// ScanApplier is the use case that folds a consumed event's facts into journeys.
type ScanApplier interface {
	Execute(ctx context.Context, cmd usecases.ApplyScansCmd) (usecases.ApplyResult, error)
}

// Returner is the ReturnToSender use case.
type Returner interface {
	Execute(ctx context.Context, cmd usecases.ReturnCmd) (usecases.ReturnResult, error)
}

// CatalogRecorder is the RecordCatalogItem use case.
type CatalogRecorder interface {
	Execute(ctx context.Context, consumer, eventID string, d repository.CatalogDimensions) (bool, error)
}

// at returns the field when set, else the CloudEvents time, else a bad payload.
func at(field time.Time, e ce.Event, name string) (time.Time, error) {
	switch {
	case !field.IsZero():
		return field.UTC(), nil
	case !e.Time().IsZero():
		return e.Time().UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%w: %s is missing and the event carries no time", errBadPayload, name)
}

// facts builds one fact per tracking number.
func facts(tracking []string, node string, typ journey.ScanType, when time.Time, pickup bool) []usecases.Fact {
	out := make([]usecases.Fact, 0, len(tracking))
	for _, tn := range tracking {
		out = append(out, usecases.Fact{TrackingNumber: shared.TrackingNumber(tn), NodeID: node, Type: typ, At: when, PickupIfLabelled: pickup})
	}
	return out
}

func applyScans(ctx context.Context, logger *slog.Logger, uc ScanApplier, consumer string, e ce.Event, fs []usecases.Fact) error {
	if len(fs) == 0 {
		return nil // nothing to apply: an event for packages this service did not label
	}
	res, err := uc.Execute(ctx, usecases.ApplyScansCmd{Consumer: consumer, EventID: e.ID(), Facts: fs})
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "skipped", res.Skipped, "applied", res.Applied,
		"stored", res.Stored, "duplicates", res.Duplicates, "closed", res.Closed, "unknown", res.Unknown)
	return nil
}

// --- vendor-connectivity ---------------------------------------------------

type vendorShipmentConfirmedData struct {
	PONumber        string    `json:"poNumber"`
	NodeID          string    `json:"nodeId"`
	TrackingNumbers []string  `json:"trackingNumbers"`
	ShippedAt       time.Time `json:"shippedAt"`
}

// VendorHandlers maps vendor-connectivity's VendorShipmentConfirmed to a
// SHIPMENT_CONFIRMED scan per tracking number.
func VendorHandlers(uc ScanApplier, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeVendorShipmentConfirmed: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[vendorShipmentConfirmedData](e)
			if err != nil {
				return err
			}
			when, err := at(d.ShippedAt, e, "shippedAt")
			if err != nil {
				return err
			}
			return applyScans(ctx, logger, uc, ConsumerVendor, e, facts(d.TrackingNumbers, d.NodeID, journey.ScanShipmentConfirmed, when, false))
		},
	}
}

// --- linehaul-transportation -----------------------------------------------

type loadDepartedData struct {
	OriginNodeID    string    `json:"originNodeId"`
	TrackingNumbers []string  `json:"trackingNumbers"`
	DepartedAt      time.Time `json:"departedAt"`
	DepartureKind   string    `json:"departureKind"`
}

type loadArrivedData struct {
	DestinationNodeID   string    `json:"destinationNodeId"`
	TrackingNumbers     []string  `json:"trackingNumbers"`
	ArrivedAt           time.Time `json:"arrivedAt"`
	NodeType            string    `json:"nodeType"`
	DestinationNodeType string    `json:"destinationNodeType"`
}

// LinehaulHandlers maps LoadDeparted to PICKUP_DEPARTURE (kind PICKUP) or
// DEPARTURE (any other kind; an absent kind is a pickup while the journey is
// LABELLED) and LoadArrived to STATION_ARRIVAL (node type DELIVERY_STATION) or
// TRANSIT_ARRIVAL (ADR 0002 A20).
func LinehaulHandlers(uc ScanApplier, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeLoadDeparted: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[loadDepartedData](e)
			if err != nil {
				return err
			}
			when, err := at(d.DepartedAt, e, "departedAt")
			if err != nil {
				return err
			}
			typ, pickupIfLabelled := journey.ScanDeparture, d.DepartureKind == ""
			if d.DepartureKind == departureKindPickup {
				typ = journey.ScanPickupDeparture
			}
			return applyScans(ctx, logger, uc, ConsumerLinehaul, e, facts(d.TrackingNumbers, d.OriginNodeID, typ, when, pickupIfLabelled))
		},
		TypeLoadArrived: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[loadArrivedData](e)
			if err != nil {
				return err
			}
			when, err := at(d.ArrivedAt, e, "arrivedAt")
			if err != nil {
				return err
			}
			nodeType := d.NodeType
			if nodeType == "" {
				nodeType = d.DestinationNodeType
			}
			typ := journey.ScanTransitArrival
			if nodeType == nodeTypeStation {
				typ = journey.ScanStationArrival
			}
			return applyScans(ctx, logger, uc, ConsumerLinehaul, e, facts(d.TrackingNumbers, d.DestinationNodeID, typ, when, false))
		},
	}
}

// --- last-mile-delivery ----------------------------------------------------

type packageOutForDeliveryData struct {
	TrackingNumber string    `json:"trackingNumber"`
	StationNodeID  string    `json:"stationNodeId"`
	At             time.Time `json:"at"`
}

type deliveryAttemptedData struct {
	TrackingNumber string    `json:"trackingNumber"`
	Outcome        string    `json:"outcome"`
	At             time.Time `json:"at"`
}

type packageReturnInitiatedData struct {
	TrackingNumber string    `json:"trackingNumber"`
	At             time.Time `json:"at"`
}

// LastMileHandlers maps PackageOutForDelivery to OUT_FOR_DELIVERY,
// DeliveryAttempted to DELIVERED or DELIVERY_FAILED (the scan takes the node of
// the journey's last applied scan, A21) and PackageReturnInitiated to
// ReturnToSender.
func LastMileHandlers(scans ScanApplier, returner Returner, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	returnHandler := func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[packageReturnInitiatedData](e)
		if err != nil {
			return err
		}
		if d.TrackingNumber == "" {
			return fmt.Errorf("%w: trackingNumber is blank", errBadPayload)
		}
		when, err := at(d.At, e, "at")
		if err != nil {
			return err
		}
		res, err := returner.Execute(ctx, usecases.ReturnCmd{
			Consumer: ConsumerLastMile, EventID: e.ID(), TrackingNumber: shared.TrackingNumber(d.TrackingNumber), ReturnedAt: when,
		})
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "skipped", res.Skipped,
			"returned", res.Returned, "unknown", res.Unknown, "closed", res.Closed)
		return nil
	}
	return map[string]HandlerFunc{
		TypePackageOutForDelivery: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[packageOutForDeliveryData](e)
			if err != nil {
				return err
			}
			when, err := at(d.At, e, "at")
			if err != nil {
				return err
			}
			return applyScans(ctx, logger, scans, ConsumerLastMile, e,
				facts(nonBlank(d.TrackingNumber), d.StationNodeID, journey.ScanOutForDelivery, when, false))
		},
		TypeDeliveryAttempted: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[deliveryAttemptedData](e)
			if err != nil {
				return err
			}
			when, err := at(d.At, e, "at")
			if err != nil {
				return err
			}
			typ := journey.ScanDeliveryFailed
			if d.Outcome == outcomeDelivered {
				typ = journey.ScanDelivered
			}
			return applyScans(ctx, logger, scans, ConsumerLastMile, e, facts(nonBlank(d.TrackingNumber), "", typ, when, false))
		},
		TypePackageReturnInitiated:        returnHandler,
		TypePackageReturnInitiatedOnRoute: returnHandler,
	}
}

// nonBlank returns the one-element list of tn, or nil for a blank value (an
// event for no package is acknowledged, not applied).
func nonBlank(tn string) []string {
	if tn == "" {
		return nil
	}
	return []string{tn}
}

// --- offer-availability ----------------------------------------------------

type catalogItemCreatedData struct {
	CatalogItemID string `json:"catalogItemId"`
	Title         string `json:"title"`
	LengthMm      int64  `json:"lengthMm"`
	WidthMm       int64  `json:"widthMm"`
	HeightMm      int64  `json:"heightMm"`
	WeightG       int64  `json:"weightG"`
	Hazmat        bool   `json:"hazmat"`
	Fragile       bool   `json:"fragile"`
}

// CatalogHandlers maps CatalogItemCreated to the CatalogDimensionsView upsert.
func CatalogHandlers(uc CatalogRecorder, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeCatalogItemCreated: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[catalogItemCreatedData](e)
			if err != nil {
				return err
			}
			recorded, err := uc.Execute(ctx, ConsumerCatalog, e.ID(), repository.CatalogDimensions{
				CatalogItemID: d.CatalogItemID, Title: d.Title, LengthMm: d.LengthMm, WidthMm: d.WidthMm,
				HeightMm: d.HeightMm, WeightG: d.WeightG, Hazmat: d.Hazmat, Fragile: d.Fragile,
			})
			if err != nil {
				return err
			}
			logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "recorded", recorded)
			return nil
		},
	}
}

// --- constructors ----------------------------------------------------------

// NewVendorConsumer reads vendor-connectivity's topic under groupID
// (VENDOR_CONSUMER_GROUP, never a literal).
func NewVendorConsumer(brokers []string, groupID string, uc ScanApplier, logger *slog.Logger) *Consumer {
	return newConsumer("vendor consumer", brokers, VendorTopic, groupID, VendorHandlers(uc, logger), logger)
}

// NewLinehaulConsumer reads linehaul-transportation's topic under groupID
// (LINEHAUL_CONSUMER_GROUP).
func NewLinehaulConsumer(brokers []string, groupID string, uc ScanApplier, logger *slog.Logger) *Consumer {
	return newConsumer("linehaul consumer", brokers, LinehaulTopic, groupID, LinehaulHandlers(uc, logger), logger)
}

// NewLastMileConsumer reads last-mile-delivery's topic under groupID
// (LASTMILE_CONSUMER_GROUP).
func NewLastMileConsumer(brokers []string, groupID string, scans ScanApplier, returner Returner, logger *slog.Logger) *Consumer {
	return newConsumer("last-mile consumer", brokers, LastMileTopic, groupID, LastMileHandlers(scans, returner, logger), logger)
}

// NewCatalogConsumer reads offer-availability's topic under groupID
// (CATALOG_CONSUMER_GROUP).
func NewCatalogConsumer(brokers []string, groupID string, uc CatalogRecorder, logger *slog.Logger) *Consumer {
	return newConsumer("catalog consumer", brokers, CatalogTopic, groupID, CatalogHandlers(uc, logger), logger)
}

// NewConsumerForTopic builds a consumer of the named handlers on an explicit
// topic, so an integration test can point the identical logic at a throwaway
// topic.
func NewConsumerForTopic(name string, brokers []string, topic, groupID string, handlers map[string]HandlerFunc, logger *slog.Logger) *Consumer {
	return newConsumer(name, brokers, topic, groupID, handlers, logger)
}
