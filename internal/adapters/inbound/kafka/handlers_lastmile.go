package kafka

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

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
