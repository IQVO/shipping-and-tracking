package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Carrier status vocabulary of POST /carrier-events (ADR 0015).
const (
	CarrierStatusPickedUp       = "PICKED_UP"
	CarrierStatusInTransit      = "IN_TRANSIT"
	CarrierStatusOutForDelivery = "OUT_FOR_DELIVERY"
	CarrierStatusDelivered      = "DELIVERED"
	CarrierStatusDeliveryFailed = "DELIVERY_FAILED"
)

// CarrierClockSkew is how far into the future a carrier event's `at` may lie
// before it is refused (ADR 0015).
const CarrierClockSkew = 5 * time.Minute

// CarrierEventCmd is the body of POST /carrier-events.
type CarrierEventCmd struct {
	CarrierID      string
	CarrierEventID string
	TrackingNumber shared.TrackingNumber
	Status         string
	At             time.Time
	ProofCode      string
	LocationCode   string
}

// ReportCarrierEvent translates an external carrier's progress report into a
// scan on the journey (ADR 0015). Unauthenticated by the standing fleet
// decision, exactly like the scans route.
type ReportCarrierEvent struct{ writer }

// NewReportCarrierEvent builds the use case.
func NewReportCarrierEvent(d Deps) *ReportCarrierEvent { return &ReportCarrierEvent{writer{d}} }

func (c CarrierEventCmd) validate(now time.Time) error {
	if !shared.ValidID(c.CarrierID) {
		return shared.Invalid("carrierId %q is not a valid id", c.CarrierID)
	}
	if !shared.ValidIDN(c.CarrierEventID, shared.MaxScanID/2) {
		return shared.Invalid("carrierEventId %q is not a valid id", c.CarrierEventID)
	}
	if !c.TrackingNumber.Valid() {
		return shared.Invalid("trackingNumber %q is not a valid id", c.TrackingNumber)
	}
	switch c.Status {
	case CarrierStatusPickedUp, CarrierStatusInTransit, CarrierStatusOutForDelivery, CarrierStatusDelivered, CarrierStatusDeliveryFailed:
	default:
		return shared.Invalid("status %q is not PICKED_UP, IN_TRANSIT, OUT_FOR_DELIVERY, DELIVERED or DELIVERY_FAILED", c.Status)
	}
	if c.At.IsZero() {
		return shared.Invalid("at is required")
	}
	if c.At.After(now.Add(CarrierClockSkew)) {
		return shared.Invalid("at %s is in the future beyond the %s clock-skew allowance", c.At.Format(time.RFC3339), CarrierClockSkew)
	}
	return nil
}

// carrierScan maps a status to its scan type. PICKED_UP is the handoff at the
// journey's stored handoff node; the other reportable statuses carry the
// carrierId as the node (the carrier is the observer).
func carrierScan(status string) (journey.ScanType, bool) {
	switch status {
	case CarrierStatusPickedUp:
		return journey.ScanCarrierHandoff, true
	case CarrierStatusOutForDelivery:
		return journey.ScanOutForDelivery, true
	case CarrierStatusDelivered:
		return journey.ScanDelivered, true
	case CarrierStatusDeliveryFailed:
		return journey.ScanDeliveryFailed, true
	}
	return "", false
}

// carrierRefusal carries a view out of a committed unit with an acknowledged
// error (the stored-not-applied semantics of RecordScan).
type carrierRefusal struct {
	view views.Journey
	err  error
}

// Execute applies the carrier event. Errors: shared.ErrValidation (bad status,
// blank ids, future `at`), ports.ErrNotFound, journey.ErrJourneyClosed,
// journey.ErrDuplicateScan, journey.ErrScanOutOfOrder and
// shared.ErrIllegalTransition (stored, not applied). IN_TRANSIT and a PICKED_UP
// for a journey already WITH_CARRIER change nothing and answer the view.
func (u *ReportCarrierEvent) Execute(ctx context.Context, cmd CarrierEventCmd) (views.Journey, error) {
	if err := cmd.validate(u.now()); err != nil {
		return views.Journey{}, err
	}
	out, err := transact(ctx, u.UoW, func(ctx context.Context) (carrierRefusal, error) {
		j, err := u.Journeys.Get(ctx, cmd.TrackingNumber)
		if err != nil {
			return carrierRefusal{}, err
		}
		return u.apply(ctx, j, cmd)
	})
	if err != nil {
		return views.Journey{}, err
	}
	return out.view, out.err
}

// carrierScanID is the dedupe key of a carrier event: one applied scan per
// (carrier, event) pair, so a re-sent report is ErrDuplicateScan (ADR 0015).
func carrierScanID(carrierID, carrierEventID string) string {
	return fmt.Sprintf("%s:%s", carrierID, carrierEventID)
}

// apply maps one validated carrier report onto the journey: the no-scan
// statuses answer the view, a re-sent report is ErrDuplicateScan, and a
// reportable status becomes a scan at the handoff node or the carrier itself.
func (u *ReportCarrierEvent) apply(ctx context.Context, j *journey.Journey, cmd CarrierEventCmd) (carrierRefusal, error) {
	typ, scans := carrierScan(cmd.Status)
	if !scans {
		// IN_TRANSIT is counted, not scanned (ADR 0015).
		return carrierRefusal{view: views.JourneyFrom(j)}, nil
	}
	if typ == journey.ScanCarrierHandoff && j.State() == journey.StateWithCarrier {
		// A PICKED_UP for a journey already in carrier custody is a no-op
		// - unless it is a re-sent report, which is ErrDuplicateScan.
		if j.HasScan(carrierScanID(cmd.CarrierID, cmd.CarrierEventID)) {
			return carrierRefusal{}, journey.ErrDuplicateScan
		}
		return carrierRefusal{view: views.JourneyFrom(j)}, nil
	}
	in := journey.ScanInput{ID: carrierScanID(cmd.CarrierID, cmd.CarrierEventID), NodeID: cmd.CarrierID, Type: typ, At: cmd.At}
	if typ == journey.ScanCarrierHandoff {
		in.NodeID = j.HandoffNodeID()
		prefix, err := u.labelPrefix(ctx, j)
		if err != nil {
			return carrierRefusal{}, err
		}
		in.DestinationPostalPrefix = prefix
	}
	res, scanErr := j.RecordScan(in, u.now())
	if !res.Changed {
		return carrierRefusal{}, scanErr
	}
	if err := u.Journeys.Save(ctx, j); err != nil {
		return carrierRefusal{}, err
	}
	if err := u.publish(ctx, res.Events); err != nil {
		return carrierRefusal{}, err
	}
	return carrierRefusal{view: views.JourneyFrom(j), err: scanErr}, nil
}
