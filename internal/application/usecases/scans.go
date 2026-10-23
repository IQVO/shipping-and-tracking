package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// RecordScanCmd is the REST request of RecordScan.
type RecordScanCmd struct {
	TrackingNumber shared.TrackingNumber
	ScanID         string
	NodeID         string
	Type           journey.ScanType
	At             time.Time
}

// RecordScan records one scan over REST.
type RecordScan struct{ writer }

// NewRecordScan builds the use case.
func NewRecordScan(d Deps) *RecordScan { return &RecordScan{writer{d}} }

// refusal carries a domain refusal out of a committed unit: a scan stored
// without moving the state is kept (the unit commits) and the caller still gets
// the error (ErrScanOutOfOrder, ErrIllegalTransition).
type refusal struct {
	view views.Journey
	err  error
}

// Execute records the scan. Errors: ports.ErrNotFound, shared.ErrValidation,
// journey.ErrJourneyClosed, journey.ErrDuplicateScan, journey.ErrScanOutOfOrder
// and shared.ErrIllegalTransition (both after the scan was stored unapplied).
func (u *RecordScan) Execute(ctx context.Context, cmd RecordScanCmd) (views.Journey, error) {
	out, err := transact(ctx, u.UoW, func(ctx context.Context) (refusal, error) {
		j, err := u.Journeys.Get(ctx, cmd.TrackingNumber)
		if err != nil {
			return refusal{}, err
		}
		res, scanErr := j.RecordScan(journey.ScanInput{ID: cmd.ScanID, NodeID: cmd.NodeID, Type: cmd.Type, At: cmd.At}, u.now())
		if !res.Changed {
			return refusal{}, scanErr
		}
		if err := u.Journeys.Save(ctx, j); err != nil {
			return refusal{}, err
		}
		if err := u.publish(ctx, res.Events); err != nil {
			return refusal{}, err
		}
		return refusal{view: views.JourneyFrom(j), err: scanErr}, nil
	})
	if err != nil {
		return views.Journey{}, err
	}
	return out.view, out.err
}

// Fact is one tracking number's share of a consumed event, ready to be a scan.
type Fact struct {
	TrackingNumber shared.TrackingNumber
	// NodeID is the node of the observation; empty means "the node of the
	// journey's last applied scan" (ADR 0002 A21).
	NodeID string
	Type   journey.ScanType
	At     time.Time
	// PickupIfLabelled promotes a DEPARTURE to PICKUP_DEPARTURE when the
	// journey is still LABELLED (a LoadDeparted without a departureKind,
	// ADR 0002 A20).
	PickupIfLabelled bool
}

// ApplyScansCmd is one consumed event: the facts of all its tracking numbers.
type ApplyScansCmd struct {
	Consumer string
	EventID  string
	Facts    []Fact
}

// ApplyResult counts what a consumed event did. Skipped is true when the
// CloudEvents id had been claimed before.
type ApplyResult struct {
	Skipped    bool
	Applied    int
	Stored     int
	Duplicates int
	Closed     int
	Unknown    int
}

// unknownNode is the node of a scan that has no node and no earlier scan.
const unknownNode = "unknown"

// ApplyScans folds the facts of one consumed event into their journeys in ONE
// unit that first claims the CloudEvents id (ADR 0002 A16).
type ApplyScans struct{ writer }

// NewApplyScans builds the use case.
func NewApplyScans(d Deps) *ApplyScans { return &ApplyScans{writer{d}} }

// Execute applies the facts. Domain refusals that a consumer must acknowledge
// (duplicate, out of order, no edge, closed) are counted, not returned; a
// validation error or an infrastructure failure is returned.
func (u *ApplyScans) Execute(ctx context.Context, cmd ApplyScansCmd) (ApplyResult, error) {
	return transact(ctx, u.UoW, func(ctx context.Context) (ApplyResult, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return ApplyResult{Skipped: err == nil}, err
		}
		var res ApplyResult
		for _, f := range cmd.Facts {
			if err := u.applyOne(ctx, cmd.EventID, f, &res); err != nil {
				return ApplyResult{}, err
			}
		}
		return res, nil
	})
}

func (u *ApplyScans) applyOne(ctx context.Context, eventID string, f Fact, res *ApplyResult) error {
	j, err := u.Journeys.Get(ctx, f.TrackingNumber)
	if ok, err := found(err); err != nil {
		return err
	} else if !ok {
		res.Unknown++
		return nil
	}
	in := factScan(eventID, f, j)
	r, scanErr := j.RecordScan(in, u.now())
	if !r.Changed {
		return count(res, scanErr)
	}
	if err := u.Journeys.Save(ctx, j); err != nil {
		return err
	}
	if err := u.publish(ctx, r.Events); err != nil {
		return err
	}
	return count(res, scanErr)
}

func factScan(eventID string, f Fact, j *journey.Journey) journey.ScanInput {
	typ, node := f.Type, f.NodeID
	if f.PickupIfLabelled && j.State() == journey.StateLabelled {
		typ = journey.ScanPickupDeparture
	}
	if node == "" {
		node = j.LastNodeID()
	}
	if node == "" {
		node = unknownNode
	}
	return journey.ScanInput{ID: eventID + ":" + string(f.TrackingNumber), NodeID: node, Type: typ, At: f.At}
}

// count classifies the outcome of one scan. It returns the error when the
// consumer must not acknowledge it.
func count(res *ApplyResult, err error) error {
	switch {
	case err == nil:
		res.Applied++
	case errors.Is(err, journey.ErrDuplicateScan):
		res.Duplicates++
	case errors.Is(err, journey.ErrJourneyClosed):
		res.Closed++
	case errors.Is(err, journey.ErrScanOutOfOrder), errors.Is(err, shared.ErrIllegalTransition):
		res.Stored++
	default:
		return err
	}
	return nil
}

// ReturnCmd is the consumed PackageReturnInitiated of last-mile-delivery.
type ReturnCmd struct {
	Consumer       string
	EventID        string
	TrackingNumber shared.TrackingNumber
	ReturnedAt     time.Time
}

// ReturnResult says what a ReturnToSender did.
type ReturnResult struct {
	Skipped  bool
	Returned bool
	Unknown  bool
	Closed   bool
}

// ReturnToSender returns an AT_STATION package to its sender (ReturnToSender).
type ReturnToSender struct{ writer }

// NewReturnToSender builds the use case.
func NewReturnToSender(d Deps) *ReturnToSender { return &ReturnToSender{writer{d}} }

// Execute returns the package. A closed journey and an unknown tracking number
// are acknowledged; a journey that is not AT_STATION yet is
// shared.ErrIllegalTransition, which the consumer retries (the failed-attempt
// scan may still be on its way, ADR 0003).
func (u *ReturnToSender) Execute(ctx context.Context, cmd ReturnCmd) (ReturnResult, error) {
	return transact(ctx, u.UoW, func(ctx context.Context) (ReturnResult, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return ReturnResult{Skipped: err == nil}, err
		}
		j, err := u.Journeys.Get(ctx, cmd.TrackingNumber)
		if ok, err := found(err); err != nil || !ok {
			return ReturnResult{Unknown: err == nil}, err
		}
		events, err := j.ReturnToSender(cmd.ReturnedAt, u.now())
		if errors.Is(err, journey.ErrJourneyClosed) {
			return ReturnResult{Closed: true}, nil
		}
		if err != nil {
			return ReturnResult{}, err
		}
		if err := u.Journeys.Save(ctx, j); err != nil {
			return ReturnResult{}, err
		}
		if err := u.publish(ctx, events); err != nil {
			return ReturnResult{}, err
		}
		return ReturnResult{Returned: true}, nil
	})
}
