package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// ApplyEtaWindowsCmd is one consumed StopEtaWindowAssigned of last-mile-delivery
// (ADR 0013): the window of one stop, for every tracking number of the stop.
// The stop key the event carries is dropped by the consumer: this context keeps
// no address-derived value of a stop.
type ApplyEtaWindowsCmd struct {
	Consumer        string
	EventID         string
	TrackingNumbers []shared.TrackingNumber
	Start, End      time.Time
	// AssignedAt is the CloudEvents time of the event: the route start instant.
	AssignedAt time.Time
}

// EtaResult counts what a consumed ETA event did. Skipped is true when the
// CloudEvents id had been claimed before.
type EtaResult struct {
	Skipped bool
	Applied int
	Stale   int
	Closed  int
	Unknown int
}

// ApplyEtaWindows stores the window on each journey in ONE unit that first
// claims the CloudEvents id.
type ApplyEtaWindows struct{ writer }

// NewApplyEtaWindows builds the use case.
func NewApplyEtaWindows(d Deps) *ApplyEtaWindows { return &ApplyEtaWindows{writer{d}} }

// Execute applies the window. An unknown tracking number, a closed journey and
// a stale window (ErrEtaStale) are counted and acknowledged; a validation error
// or an infrastructure failure is returned.
func (u *ApplyEtaWindows) Execute(ctx context.Context, cmd ApplyEtaWindowsCmd) (EtaResult, error) {
	return transact(ctx, u.UoW, func(ctx context.Context) (EtaResult, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return EtaResult{Skipped: err == nil}, err
		}
		var res EtaResult
		for _, tn := range cmd.TrackingNumbers {
			if err := u.applyOne(ctx, tn, cmd, &res); err != nil {
				return EtaResult{}, err
			}
		}
		return res, nil
	})
}

func (u *ApplyEtaWindows) applyOne(ctx context.Context, tn shared.TrackingNumber, cmd ApplyEtaWindowsCmd, res *EtaResult) error {
	j, err := u.Journeys.Get(ctx, tn)
	if ok, err := found(err); err != nil {
		return err
	} else if !ok {
		res.Unknown++
		return nil
	}
	events, err := j.AssignEta(cmd.Start, cmd.End, cmd.AssignedAt, u.now())
	switch {
	case errors.Is(err, journey.ErrJourneyClosed):
		res.Closed++
		return nil
	case errors.Is(err, journey.ErrEtaStale):
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
