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

// SweepUncollected is the pickup hold policy (ADR 0016): a package nobody
// collected before its hold deadline returns to the sender. It runs in the
// SweepSilence shape - paged candidates, each journey reloaded and closed in
// its own unit.
type SweepUncollected struct {
	writer
	// PageSize is how many candidates one page holds (SweepPageSize by default).
	PageSize int
}

// NewSweepUncollected builds the use case.
func NewSweepUncollected(d Deps) *SweepUncollected {
	return &SweepUncollected{writer: writer{d}, PageSize: SweepPageSize}
}

// UncollectedSweepResult counts what one sweep pass did.
type UncollectedSweepResult struct {
	Returned   int
	Conflicts  int
	Considered int
}

// Execute walks every expired-hold page once.
func (u *SweepUncollected) Execute(ctx context.Context) (UncollectedSweepResult, error) {
	now := u.now()
	var res UncollectedSweepResult
	filter := repository.UncollectedFilter{Before: now, Limit: u.PageSize}
	for {
		page, err := u.Journeys.ListUncollected(ctx, filter)
		if err != nil {
			return res, err
		}
		res.Considered += len(page)
		for _, j := range page {
			if err := u.returnOne(ctx, j.TrackingNumber(), now, &res); err != nil {
				return res, err
			}
		}
		if len(page) < u.PageSize {
			return res, nil
		}
		last := page[len(page)-1]
		filter.After = repository.UncollectedCursor{HoldUntil: last.HoldUntil(), TrackingNumber: string(last.TrackingNumber())}
	}
}

func (u *SweepUncollected) returnOne(ctx context.Context, tn shared.TrackingNumber, now time.Time, res *UncollectedSweepResult) error {
	kind, err := transact(ctx, u.UoW, func(ctx context.Context) (string, error) {
		j, err := u.Journeys.Get(ctx, tn)
		if err != nil {
			return "", err
		}
		if j.State() != journey.StateReadyForCollection || j.HoldUntil().After(now) {
			return "", nil // the page was stale; the reload decides
		}
		events, err := j.ReturnUncollected(now, now)
		if err != nil {
			return "", err
		}
		if err := u.Journeys.Save(ctx, j); err != nil {
			return "", err
		}
		return "returned", u.publish(ctx, events)
	})
	switch {
	case errors.Is(err, ports.ErrConcurrentUpdate):
		res.Conflicts++
	case errors.Is(err, ports.ErrNotFound):
		// purged between the page and the reload: nothing to do
	case err != nil:
		return err
	case kind == "returned":
		res.Returned++
	}
	return nil
}
