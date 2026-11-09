package reportshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// fakeReader answers fixed rows and records the range it was asked for.
type fakeReader struct {
	touches  []report.TouchesDay
	outcomes []report.OutcomeDay
	last     *time.Time
	err      error
	asked    report.Range
}

func (f *fakeReader) PackageTouches(_ context.Context, r report.Range) ([]report.TouchesDay, error) {
	f.asked = r
	return f.touches, f.err
}

func (f *fakeReader) DeliveryOutcomes(_ context.Context, r report.Range) ([]report.OutcomeDay, error) {
	f.asked = r
	return f.outcomes, f.err
}

func (f *fakeReader) LastEventAt(context.Context) (*time.Time, error) { return f.last, f.err }

type fakePing struct{ err error }

func (p fakePing) Ping(context.Context) error { return p.err }

var (
	now  = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)
	nov3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
)

func serve(t *testing.T, s *reportshttp.Server, method, target string) (int, string, map[string]any) {
	t.Helper()
	if s.Now == nil {
		s.Now = func() time.Time { return now }
	}
	h := reportshttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, target, rec.Body.String(), err)
	}
	return rec.Code, rec.Header().Get("Content-Type"), body
}

func TestPackageTouchesReport(t *testing.T) {
	r := &fakeReader{touches: []report.TouchesDay{{Day: nov3, Tier: "NEXT_DAY", PackagesDelivered: 4, Touches: 18, P50: 4.5, P90: 6}}}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/package-touches?from=2026-11-01&to=2026-11-07")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{"from": "2026-11-01", "to": "2026-11-07", "days": []any{map[string]any{
		"day": "2026-11-03", "tier": "NEXT_DAY", "packagesDelivered": 4.0, "meanTouchesPerPackage": 4.5,
		"p50TouchesPerPackage": 4.5, "p90TouchesPerPackage": 6.0,
	}}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if !r.asked.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !r.asked.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked range = %+v, want [11-01, 11-08)", r.asked)
	}
}

func TestDeliveryOutcomesReport(t *testing.T) {
	r := &fakeReader{outcomes: []report.OutcomeDay{
		{Day: nov3, Delivered: 3, FirstAttemptDelivered: 2, FailedAttempts: 1, Returned: 1},
		{Day: nov3.Add(24 * time.Hour), Lost: 2},
	}}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/delivery-outcomes")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := map[string]any{"from": "2026-10-10", "to": "2026-11-08", "days": []any{
		map[string]any{"day": "2026-11-03", "packagesDelivered": 3.0, "firstAttemptDelivered": 2.0, "packagesReturnedToSender": 1.0,
			"packagesLost": 0.0, "firstAttemptSuccessRate": 0.5, "meanAttemptsPerDeliveredPackage": 4.0 / 3, "lostRate": 0.0,
			"returnToSenderRate": 0.25},
		map[string]any{"day": "2026-11-04", "packagesDelivered": 0.0, "firstAttemptDelivered": 0.0, "packagesReturnedToSender": 0.0,
			"packagesLost": 2.0, "firstAttemptSuccessRate": 0.0, "meanAttemptsPerDeliveredPackage": nil, "lostRate": 1.0,
			"returnToSenderRate": 0.0},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
}

func TestEmptyReportsAreEmptyArrays(t *testing.T) {
	for _, path := range []string{"/reports/package-touches", "/reports/delivery-outcomes"} {
		_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, path)
		if days, ok := body["days"].([]any); !ok || len(days) != 0 {
			t.Errorf("%s days = %#v, want []", path, body["days"])
		}
	}
}

func TestFreshness(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/freshness")
	if v, ok := body["asOf"]; !ok || v != nil || body["lagSeconds"] != nil {
		t.Fatalf("before the first event = %v, want explicit nulls", body)
	}
	asOf := now.Add(-90 * time.Second)
	_, _, body = serve(t, &reportshttp.Server{Reader: &fakeReader{last: &asOf}}, http.MethodGet, "/reports/freshness")
	if body["asOf"] != "2026-11-08T18:28:30Z" || body["lagSeconds"] != 90.0 {
		t.Fatalf("freshness = %v", body)
	}
}

func assertProblem(t *testing.T, code int, ct string, body map[string]any, wantCode int, wantType string) {
	t.Helper()
	if code != wantCode || ct != "application/problem+json" || body["type"] != wantType || body["status"] != float64(wantCode) {
		t.Fatalf("got %d %s %v, want %d problem %s", code, ct, body, wantCode, wantType)
	}
}

func TestInvalidRangeIsAProblem(t *testing.T) {
	for _, q := range []string{"?from=2026-11-05&to=2026-11-04", "?from=nope", "?to=2026-13-01", "?from=2025-01-01&to=2026-11-01"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/delivery-outcomes"+q)
		assertProblem(t, code, ct, body, http.StatusBadRequest, "invalid-report-range")
	}
}

func TestReaderFailureIsAnOpaque500(t *testing.T) {
	r := &fakeReader{err: errors.New("pq: password authentication failed for secret-role")}
	for _, path := range []string{"/reports/package-touches", "/reports/delivery-outcomes", "/reports/freshness"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, path)
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
		if strings.Contains(body["detail"].(string), "secret") {
			t.Fatalf("%s leaked the cause: %v", path, body)
		}
	}
}

func TestUnknownPathAndWriteMethodsAreProblems(t *testing.T) {
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/nope")
	assertProblem(t, code, ct, body, http.StatusNotFound, "not-found")
	code, ct, body = serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodPost, "/reports/freshness")
	assertProblem(t, code, ct, body, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestHealthAndReadiness(t *testing.T) {
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{Ping: fakePing{}}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v", code, body)
	}
	code, ct, body := serve(t, &reportshttp.Server{Ping: fakePing{err: errors.New("down")}}, http.MethodGet, "/readyz")
	assertProblem(t, code, ct, body, http.StatusServiceUnavailable, "dependency-unavailable")
	draining := &reportshttp.Server{}
	draining.SetNotReady()
	if code, _, body := serve(t, draining, http.MethodGet, "/readyz"); code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("draining readyz = %d %v", code, body)
	}
}
