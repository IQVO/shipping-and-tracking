package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// RelayDeliveryPointFlagCmd is one consumed DeliveryPointFlagged of
// last-mile-delivery (ADR 0014): the delivery point behind a stop failed twice.
// The stop key the event carries is dropped by the consumer: this context keeps
// no address-derived value of a stop.
type RelayDeliveryPointFlagCmd struct {
	Consumer        string
	EventID         string
	TrackingNumbers []shared.TrackingNumber
	Reason          string
	FlaggedAt       time.Time
}

// PointFlagResult counts what a consumed flag event did. Skipped is true when
// the CloudEvents id had been claimed before.
type PointFlagResult struct {
	Skipped bool
	Applied int
	Stale   int
	Closed  int
	Unknown int
}

// RelayDeliveryPointFlag stores the flag on each journey of the stop in ONE
// unit that first claims the CloudEvents id, and publishes
// PackageDeliveryPointFlagged per journey so customer-ordering can warn the
// shopper by orderId.
type RelayDeliveryPointFlag struct{ writer }

// NewRelayDeliveryPointFlag builds the use case.
func NewRelayDeliveryPointFlag(d Deps) *RelayDeliveryPointFlag {
	return &RelayDeliveryPointFlag{writer{d}}
}

// Execute relays the flag. An unknown tracking number, a closed journey and a
// stale flag (ErrPointFlagStale) are counted and acknowledged; a validation
// error or an infrastructure failure is returned.
func (u *RelayDeliveryPointFlag) Execute(ctx context.Context, cmd RelayDeliveryPointFlagCmd) (PointFlagResult, error) {
	return transact(ctx, u.UoW, func(ctx context.Context) (PointFlagResult, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return PointFlagResult{Skipped: err == nil}, err
		}
		var res PointFlagResult
		for _, tn := range cmd.TrackingNumbers {
			if err := u.applyOne(ctx, tn, cmd, &res); err != nil {
				return PointFlagResult{}, err
			}
		}
		return res, nil
	})
}

func (u *RelayDeliveryPointFlag) applyOne(ctx context.Context, tn shared.TrackingNumber, cmd RelayDeliveryPointFlagCmd, res *PointFlagResult) error {
	j, err := u.Journeys.Get(ctx, tn)
	if ok, err := found(err); err != nil {
		return err
	} else if !ok {
		res.Unknown++
		return nil
	}
	events, err := j.FlagDeliveryPoint(cmd.Reason, cmd.FlaggedAt, u.now())
	switch {
	case errors.Is(err, journey.ErrJourneyClosed):
		res.Closed++
		return nil
	case errors.Is(err, journey.ErrPointFlagStale):
		res.Stale++
		return nil
	case err != nil:
		return err
	}
	if err := u.Journeys.Save(ctx, j); err != nil {
		return err
	}
	if err := u.publish(ctx, events); err != nil {
		return err
	}
	res.Applied++
	return nil
}
