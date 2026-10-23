package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// SweepPageSize is how many candidate journeys one ListStale page returns.
const SweepPageSize = 200

// SweepResult counts what one sweep did. Conflicts are journeys that changed
// under the sweep; the next tick sees them again.
type SweepResult struct {
	Raised    int
	Lost      int
	Conflicts int
}

// SweepSilence is the ExceptionOnSilence policy (RaiseException and, after the
// escalation window, DeclareLost; ADR 0002 A7, A8).
type SweepSilence struct {
	writer
	// PageSize is how many candidates one page holds (SweepPageSize by default).
	PageSize int
}

// NewSweepSilence builds the use case.
func NewSweepSilence(d Deps) *SweepSilence {
	return &SweepSilence{writer: writer{d}, PageSize: SweepPageSize}
}

// Execute walks every candidate page once. Each journey is reloaded and
// decided in its own unit.
func (u *SweepSilence) Execute(ctx context.Context) (SweepResult, error) {
	now := u.now()
	var res SweepResult
	filter := repository.StaleFilter{Before: now.Add(-journey.MinSilenceWindow()), Limit: u.PageSize}
	for {
		page, err := u.Journeys.ListStale(ctx, filter)
		if err != nil {
			return res, err
		}
		for _, j := range page {
			if err := u.decide(ctx, j.TrackingNumber(), now, &res); err != nil {
				return res, err
			}
		}
		if len(page) < u.PageSize {
			return res, nil
		}
		last := page[len(page)-1]
		filter.After = repository.StaleCursor{LastScanAt: last.LastScanAt(), TrackingNumber: string(last.TrackingNumber())}
	}
}

func (u *SweepSilence) decide(ctx context.Context, tn shared.TrackingNumber, now time.Time, res *SweepResult) error {
	kind, err := transact(ctx, u.UoW, func(ctx context.Context) (string, error) {
		j, err := u.Journeys.Get(ctx, tn)
		if err != nil {
			return "", err
		}
		var events []shared.Event
		kind := ""
		switch {
		case j.Lost(now):
			events, err = j.DeclareLost(now, now)
			kind = "lost"
		case j.Silent(now):
			events, err = j.RaiseException(now)
			kind = "raised"
		default:
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if err := u.Journeys.Save(ctx, j); err != nil {
			return "", err
		}
		return kind, u.publish(ctx, events)
	})
	switch {
	case errors.Is(err, ports.ErrConcurrentUpdate):
		res.Conflicts++
	case errors.Is(err, ports.ErrNotFound):
		// purged between the page and the reload: nothing to do
	case err != nil:
		return err
	case kind == "lost":
		res.Lost++
	case kind == "raised":
		res.Raised++
	}
	return nil
}
