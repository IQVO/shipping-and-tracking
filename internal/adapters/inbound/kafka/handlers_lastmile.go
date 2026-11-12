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

// stopEtaWindowAssignedData is the tolerant local copy of last-mile-delivery's
// StopEtaWindowAssigned (ADR 0013). stopNo, stopKey and routeId are not used:
// the stop key is an opaque stop identifier this context does not keep.
type stopEtaWindowAssignedData struct {
	TrackingNumbers []string  `json:"trackingNumbers"`
	EtaStart        time.Time `json:"etaStart"`
	EtaEnd          time.Time `json:"etaEnd"`
}

// deliveryPointFlaggedData is the tolerant local copy of last-mile-delivery's
// DeliveryPointFlagged (ADR 0014). stopKey and failures are not used: the stop
// key is an opaque stop identifier this context does not keep.
type deliveryPointFlaggedData struct {
	TrackingNumbers []string  `json:"trackingNumbers"`
	Reason          string    `json:"reason"`
	FlaggedAt       time.Time `json:"flaggedAt"`
}

// pointFlagTrackingNumbers validates the payload: a stop has at least one
// package, none of them blank, and the reason is one of the two the producer
// publishes.
func pointFlagTrackingNumbers(d deliveryPointFlaggedData) ([]shared.TrackingNumber, error) {
	if len(d.TrackingNumbers) == 0 {
		return nil, fmt.Errorf("%w: trackingNumbers is empty", errBadPayload)
	}
	if d.Reason != "NOT_HOME" && d.Reason != "ACCESS_PROBLEM" {
		return nil, fmt.Errorf("%w: reason must be NOT_HOME or ACCESS_PROBLEM", errBadPayload)
	}
	out := make([]shared.TrackingNumber, len(d.TrackingNumbers))
	for i, tn := range d.TrackingNumbers {
		if tn == "" {
			return nil, fmt.Errorf("%w: trackingNumbers holds a blank value", errBadPayload)
		}
		out[i] = shared.TrackingNumber(tn)
	}
	return out, nil
}

// flagHandler maps DeliveryPointFlagged to the delivery-point flag of every
// package of the stop.
func flagHandler(flagger PointFlagger, logger *slog.Logger) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[deliveryPointFlaggedData](e)
		if err != nil {
			return err
		}
		tns, err := pointFlagTrackingNumbers(d)
		if err != nil {
			return err
		}
		when, err := at(d.FlaggedAt, e, "flaggedAt")
		if err != nil {
			return err
		}
		res, err := flagger.Execute(ctx, usecases.RelayDeliveryPointFlagCmd{
			Consumer: ConsumerLastMile, EventID: e.ID(), TrackingNumbers: tns,
			Reason: d.Reason, FlaggedAt: when,
		})
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "skipped", res.Skipped, "applied", res.Applied,
			"stale", res.Stale, "closed", res.Closed, "unknown", res.Unknown)
		return nil
	}
}

// etaHandler maps StopEtaWindowAssigned to the day-of ETA window of every
// package of the stop. assignedAt is the CloudEvents time: the route start.
func etaHandler(etas EtaApplier, logger *slog.Logger) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[stopEtaWindowAssignedData](e)
		if err != nil {
			return err
		}
		tns, err := etaTrackingNumbers(d)
		if err != nil {
			return err
		}
		assignedAt, err := at(time.Time{}, e, "time")
		if err != nil {
			return err
		}
		res, err := etas.Execute(ctx, usecases.ApplyEtaWindowsCmd{
			Consumer: ConsumerLastMile, EventID: e.ID(), TrackingNumbers: tns,
			Start: d.EtaStart.UTC(), End: d.EtaEnd.UTC(), AssignedAt: assignedAt,
		})
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "skipped", res.Skipped, "applied", res.Applied,
			"stale", res.Stale, "closed", res.Closed, "unknown", res.Unknown)
		return nil
	}
}

// etaTrackingNumbers validates the payload: a stop has at least one package,
// none of them blank, and the window has a positive width.
func etaTrackingNumbers(d stopEtaWindowAssignedData) ([]shared.TrackingNumber, error) {
	if len(d.TrackingNumbers) == 0 {
		return nil, fmt.Errorf("%w: trackingNumbers is empty", errBadPayload)
	}
	if d.EtaStart.IsZero() || d.EtaEnd.IsZero() || !d.EtaStart.Before(d.EtaEnd) {
		return nil, fmt.Errorf("%w: etaStart must be before etaEnd", errBadPayload)
	}
	out := make([]shared.TrackingNumber, len(d.TrackingNumbers))
	for i, tn := range d.TrackingNumbers {
		if tn == "" {
			return nil, fmt.Errorf("%w: trackingNumbers holds a blank value", errBadPayload)
		}
		out[i] = shared.TrackingNumber(tn)
	}
	return out, nil
}

// LastMileHandlers maps PackageOutForDelivery to OUT_FOR_DELIVERY,
// DeliveryAttempted to DELIVERED or DELIVERY_FAILED (the scan takes the node of
// the journey's last applied scan, A21), PackageReturnInitiated to
// ReturnToSender and StopEtaWindowAssigned to the day-of ETA window.
func LastMileHandlers(scans ScanApplier, returner Returner, etas EtaApplier, flagger PointFlagger, logger *slog.Logger) map[string]HandlerFunc {
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
		TypeStopEtaWindowAssigned:         etaHandler(etas, logger),
		TypeDeliveryPointFlagged:          flagHandler(flagger, logger),
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
