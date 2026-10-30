package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// The boot migrations dial MIGRATIONS_DATABASE_URL (a direct DSN where
// DATABASE_URL points at a pooler), not DATABASE_URL. The two DSNs name
// different closed ports; the logged retry line must carry the migrations one
// only. A short deadline, not a cancelled context, keeps the real cause in the
// log (a cancelled context makes the error wrap only ctx.Err()).
func TestBuildDeps_MigrationsUseTheirOwnDSN(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _, err := buildDeps(ctx, logger, "postgres://u@127.0.0.1:2/runtime", "postgres://u@127.0.0.1:1/migrations")
	if err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
	logged := buf.String()
	if !strings.Contains(logged, "retrying") || !strings.Contains(logged, "127.0.0.1:1") {
		t.Fatalf("the migration step did not dial MIGRATIONS_DATABASE_URL: %s", logged)
	}
	if strings.Contains(logged, "127.0.0.1:2") {
		t.Fatalf("the migration step dialled DATABASE_URL instead: %s", logged)
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("ST_A2A_TEST_SET", "v")
	t.Setenv("ST_A2A_TEST_EMPTY", "")
	if got := getenv("ST_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("ST_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
