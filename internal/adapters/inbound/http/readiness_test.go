package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
)

func readyzOf(t *testing.T, g *inbound.Readiness) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	inbound.NewRouter(&inbound.Server{Readiness: g}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rec.Code, rec.Body.String()
}

func TestReadyzReflectsTheDrainGateAndTheDependencies(t *testing.T) {
	var pgErr, kafkaErr error
	calls := 0
	g := inbound.NewReadiness(time.Nanosecond, time.Second,
		inbound.Dependency{Name: "postgres", Check: func(context.Context) error { calls++; return pgErr }},
		inbound.Dependency{Name: "kafka", Check: func(context.Context) error { return kafkaErr }},
	)
	if code, body := readyzOf(t, g); code != 200 || body != "{\"status\":\"ready\"}\n" {
		t.Fatalf("ready = %d %s", code, body)
	}
	kafkaErr = errors.New("broker down")
	time.Sleep(time.Millisecond)
	code, body := readyzOf(t, g)
	if code != 503 || !strings.Contains(body, `"type":"dependency-unavailable"`) || !strings.Contains(body, "kafka") || strings.Contains(body, "postgres") {
		t.Fatalf("kafka down = %d %s", code, body)
	}
	kafkaErr, pgErr = nil, errors.New("down")
	time.Sleep(time.Millisecond)
	if code, body := readyzOf(t, g); code != 503 || !strings.Contains(body, "postgres") {
		t.Fatalf("pg down = %d %s", code, body)
	}
	pgErr = nil
	time.Sleep(time.Millisecond)
	g.SetNotReady()
	if code, body := readyzOf(t, g); code != 503 || !strings.Contains(body, `"not_ready"`) {
		t.Fatalf("draining = %d %s", code, body)
	}
	if g.Ready() || calls == 0 {
		t.Fatal("drain gate must be flipped and the checks must have run")
	}
}

func TestReadinessVerdictIsCachedWithinTheTTL(t *testing.T) {
	calls := 0
	g := inbound.NewReadiness(time.Hour, 0, inbound.Dependency{Name: "postgres", Check: func(context.Context) error { calls++; return nil }})
	for range 5 {
		g.Failing(context.Background())
	}
	if calls != 1 {
		t.Fatalf("checks ran %d times inside the ttl, want 1", calls)
	}
}

func TestNilReadinessIsReady(t *testing.T) {
	var g *inbound.Readiness
	g.SetNotReady()
	if !g.Ready() || g.Failing(context.Background()) != nil {
		t.Fatal("a nil gate is always ready")
	}
}
