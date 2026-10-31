// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error, and the liveness
// and readiness endpoints. It has no auth middleware (fleet decision
// 2026-09-11; internal/architecture's TestNoAuthMiddlewareReintroduced). No
// request or response carries a ship-to address (ADR 0002 A9).
package http

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/requestlog"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
)

// Route names of the idempotent POSTs (the idempotency_keys.route values).
const (
	RouteIssueLabel = "POST /labels"
	RouteVoidLabel  = "POST /labels/{labelRef}:void"
	RouteRecordScan = "POST /journeys/{trackingNumber}/scans"
)

// Server holds the use cases the handlers call.
type Server struct {
	IssueLabel  *usecases.IssueLabel
	VoidLabel   *usecases.VoidLabel
	RecordScan  *usecases.RecordScan
	GetJourney  *usecases.GetJourney
	Idempotency func(route string) func(http.Handler) http.Handler // nil leaves the POSTs unwrapped (handler unit tests)
	Readiness   *Readiness                                         // nil means always ready
}

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root passes no service name.
const DefaultServiceName = "shipping-and-tracking"

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml. A nil
// logger defaults to slog.Default(); an empty serviceName to DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, then otelchi
// (the server span, named after the chi route pattern, so one span name per
// route and not per tracking number), then the request-duration histogram, then
// RequestLogger (it runs inside the span, so its record carries trace_id and
// span_id), then Recoverer. telemetry.Setup must have run before this call:
// otelchimetric binds the global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the route-scoped idempotency
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, notFoundResource, "no resource matches the requested path")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}, "the method is not supported on this path")
	})

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent(RouteIssueLabel)).Post("/labels", s.handleIssueLabel)
	r.With(s.idempotent(RouteVoidLabel)).Post("/labels/{labelRef}:void", s.handleVoidLabel)
	r.Get("/journeys/{trackingNumber}", s.handleGetJourney)
	r.With(s.idempotent(RouteRecordScan)).Post("/journeys/{trackingNumber}/scans", s.handleRecordScan)
	return r
}

func (s *Server) idempotent(route string) func(http.Handler) http.Handler {
	if s.Idempotency == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return s.Idempotency(route)
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// pathParam reads a chi path parameter. chi matches "{labelRef}:void" with the
// parameter ending at the colon, so the value is the bare reference.
func pathParam(r *http.Request, name string) string {
	return strings.TrimSpace(chi.URLParam(r, name))
}
