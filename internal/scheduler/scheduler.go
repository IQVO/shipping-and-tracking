// Package scheduler runs the context's periodic jobs (the silence sweep, the
// ship-to purge and the housekeeping of the technical tables) on a fixed
// interval inside the one service process. A job that fails is logged and tried
// again on the next tick; it never stops the loop. A job runs to completion
// before the next tick is awaited, so two runs of one job never overlap in this
// process.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Job is one periodic piece of work.
type Job struct {
	Name     string
	Interval time.Duration
	// RunOnStart runs the job once immediately, then every Interval.
	RunOnStart bool
	Run        func(ctx context.Context) (summary string, err error)
}

// Run blocks until ctx is cancelled, running j every j.Interval. A non-positive
// interval is a programming error and returns immediately.
func Run(ctx context.Context, logger *slog.Logger, j Job) {
	if j.Interval <= 0 {
		logger.Error("scheduler: job has no positive interval; not running", "job", j.Name)
		return
	}
	if j.RunOnStart {
		once(ctx, logger, j)
	}
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			once(ctx, logger, j)
		}
	}
}

func once(ctx context.Context, logger *slog.Logger, j Job) {
	if ctx.Err() != nil {
		return
	}
	summary, err := j.Run(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logger.ErrorContext(ctx, "scheduled job failed; trying again on the next tick", "job", j.Name, "error", err)
		}
		return
	}
	logger.InfoContext(ctx, "scheduled job finished", "job", j.Name, "result", summary)
}
