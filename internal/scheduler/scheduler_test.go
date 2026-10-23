package scheduler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/scheduler"
)

func runFor(t *testing.T, j scheduler.Job, d time.Duration) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	done := make(chan struct{})
	go func() { scheduler.Run(ctx, logger, j); close(done) }()
	select {
	case <-done:
	case <-time.After(d + 2*time.Second):
		t.Fatal("Run did not return after the context ended")
	}
	return buf.String()
}

func TestAJobRunsOnEveryTickAndLogsItsSummary(t *testing.T) {
	var n atomic.Int32
	logs := runFor(t, scheduler.Job{Name: "sweep", Interval: 10 * time.Millisecond, Run: func(context.Context) (string, error) {
		n.Add(1)
		return "raised=2", nil
	}}, 200*time.Millisecond)
	if n.Load() < 3 || !strings.Contains(logs, "job=sweep") || !strings.Contains(logs, "raised=2") {
		t.Fatalf("ran %d times; logs %s", n.Load(), logs)
	}
}

func TestRunOnStartRunsBeforeTheFirstTick(t *testing.T) {
	var n atomic.Int32
	runFor(t, scheduler.Job{Name: "purge", Interval: time.Hour, RunOnStart: true, Run: func(context.Context) (string, error) {
		n.Add(1)
		return "", nil
	}}, 50*time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("ran %d times, want exactly the start run", n.Load())
	}
}

func TestWithoutRunOnStartNothingRunsBeforeTheFirstTick(t *testing.T) {
	var n atomic.Int32
	runFor(t, scheduler.Job{Name: "purge", Interval: time.Hour, Run: func(context.Context) (string, error) {
		n.Add(1)
		return "", nil
	}}, 50*time.Millisecond)
	if n.Load() != 0 {
		t.Fatalf("ran %d times", n.Load())
	}
}

func TestAFailingJobIsLoggedAndRetriedOnTheNextTick(t *testing.T) {
	var n atomic.Int32
	logs := runFor(t, scheduler.Job{Name: "flaky", Interval: 10 * time.Millisecond, Run: func(context.Context) (string, error) {
		if n.Add(1) == 1 {
			return "", errors.New("database down")
		}
		return "ok", nil
	}}, 200*time.Millisecond)
	if n.Load() < 2 || !strings.Contains(logs, "database down") || !strings.Contains(logs, "result=ok") {
		t.Fatalf("ran %d times; logs %s", n.Load(), logs)
	}
}

func TestACancelledContextEndsQuietly(t *testing.T) {
	logs := runFor(t, scheduler.Job{Name: "slow", Interval: time.Hour, RunOnStart: true, Run: func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}, 30*time.Millisecond)
	if strings.Contains(logs, "failed") {
		t.Fatalf("a shutdown is not a failure: %s", logs)
	}
}

func TestANonPositiveIntervalIsRefused(t *testing.T) {
	var n atomic.Int32
	logs := runFor(t, scheduler.Job{Name: "bad", Run: func(context.Context) (string, error) { n.Add(1); return "", nil }}, 20*time.Millisecond)
	if n.Load() != 0 || !strings.Contains(logs, "no positive interval") {
		t.Fatalf("ran %d; logs %s", n.Load(), logs)
	}
}
