# ADR 0008: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26). Supersedes the "Not built" paragraph of ADR 0005 (no telemetry exporter, no metrics).

## Context

shipping-and-tracking logged JSON with `log/slog` and nothing else: no trace or span id on a log line, no request
id, no request log, no metrics, no traces, no `traceparent` on the events it publishes and no OpenTelemetry settings
in the Helm chart. The warehouse-systems services (inventory-storage ADR 0016, wes-work-planning ADR 0013, "standard
metrics convention") and customer-ordering (ADR 0007) already share one pipeline: pods export OTLP/gRPC to the
in-cluster Collector (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger (traces) and a
Prometheus exporter (metrics); logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a package could not be followed from the REST call through the Kafka event to the consumer in
another service, and no dashboard could show label throughput, errors or latency. A service-specific setup was
rejected (every repo should answer the same query the same way), as were auto-instrumentation agents (they cannot
follow a trace across `segmentio/kafka-go` nor name spans by chi route pattern).

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry`: `Setup` (OTLP/gRPC traces and metrics, W3C
   TraceContext + Baggage, Go runtime metrics; lazy exporters, so no Collector means dropped telemetry, never a
   blocked boot or request; returns a shutdown func) and `WithTraceContext` (stamps `trace_id`/`span_id`). Only the
   composition roots and other adapters import it; domain and application never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`, `LOG_LEVEL`. Request and
   message paths use the `*Context` slog variants (the MCP tool error log was the one bare call and was converted).
3. **HTTP middleware order** on both chi routers (REST and MCP): `RequestID` -> `otelchi` -> `otelchimetric` ->
   `RequestLogger` -> `Recoverer`; `rejectNUL` and the idempotency middleware stay after them. `telemetry.Setup` runs
   before the routers are built and is flushed last. The request line carries `method`, `route` (the chi pattern,
   never the raw path), `status`, `duration_ms`, `bytes`, `request_id`; 5xx ERROR, 4xx WARN, else INFO; probes are
   not logged. `RequestLogger` lives in `internal/adapters/inbound/requestlog` because `cmd/mcp` may not import the
   REST adapter (`TestMCPServerIsReadOnly`).
4. **Traces over Kafka.** `postgres.Outbox.Append` stamps `traceparent`/`tracestate` into the stored outbox headers
   inside the transaction (`outbox_trace.go`). The relay publishes them unchanged as Kafka headers; every inbound
   consumer extracts them and runs each handling attempt in a consumer span `<topic> process`. CloudEvents attributes
   and bodies are untouched (ADR 0004); `OutboxRepo.Insert` (raw path) stamps nothing.
5. **Environment.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME` (defaults `shipping-and-tracking`,
   `shipping-and-tracking-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`, `LOG_LEVEL`.
6. **Chart.** `otel.{enabled,endpoint,serviceName}` and `environment` render the env for the api and the mcp
   component (`<name>-mcp`); `SERVICE_VERSION` is `image.tag`, or the appVersion when empty. `extraEnv` and
   `mcp.extraEnv` refuse the four chart-owned names. The frontend component is static nginx with no Go process and
   renders no env, so it is skipped. `mcp.environment` now defaults to `production` and `mcp.serviceVersion` to
   empty (follow the image tag), aligning the component with the api.
7. **Tier 2 business metric.** One counter, `shipping.labels.issued`, unit `{label}`, attribute `outcome`
   (`issued` | `rejected`) and, on a rejection, `reason` (the problem slug: `validation-failed`,
   `ship-to-unavailable`, `label-already-issued`, `tracking-number-taken`, `concurrent-update`, `internal-error`).
   `ports.LabelMetrics` is the application port, called once per attempt from `IssueLabel.Execute`; the OTel
   implementation lives in the telemetry adapter and is injected from `cmd/api`; a nil `Deps.Metrics` is a no-op.
   Why this one: issuing a label is the context's primary command (ADR 0001, `POST /labels`): it is the moment a
   package enters the network, and its failure modes (customer-ordering unreachable, duplicate issue, collisions)
   are the operational signals a human acts on. Idempotent replays are answered by the middleware and never reach
   the use case, so they are not attempts. Scans and voids are not counted: one counter is the standard.

### Assumption (A-OBS-1)

The reference's "mcp ENVIRONMENT/SERVICE_VERSION" chart defaults were adopted as in customer-ordering. Dashboards and
alerts live in warehouse-infra and are generated per deployed context from the instrument name above.

## Consequences

- A request, its logs, the events it enqueued and the consumers in other services share one trace id.
- Request rate, errors and duration come from `http.server.request.duration` with no hand-rolled counter.
- New log calls on request/message paths must use a `*Context` variant.
- A retried message shows as several consumer spans under one parent; the relay adds no span of its own.
- The ship-to address is never an attribute of a span, metric or log: the counter carries only `outcome`/`reason`.
