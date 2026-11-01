---
id: observability
title: Observability
sidebar_label: Observability
description: "OpenTelemetry instruments, spans, log fields, exporter variables, and the dashboard and alert rules that exist for Shipping & Tracking."
---

# Observability

The service follows the fleet observability standard (ADR 0008): the pod pushes OTLP/gRPC to a Collector (traces to Jaeger, metrics to Prometheus), and writes JSON logs to stdout for Alloy and Loki. Telemetry export never blocks: with no Collector reachable the service loses telemetry and nothing else (`internal/adapters/outbound/telemetry/telemetry.go`).

## Exporter configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP/gRPC endpoint for traces and metrics. A bare `host:port` is turned into `http://host:port` |
| `OTEL_SERVICE_NAME` | `shipping-and-tracking` (`-mcp`, `-a2a` for the other binaries) | `service.name` |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |
| `LOG_LEVEL` | `info` | slog level |

`OTEL_RESOURCE_ATTRIBUTES` is honoured as well (the SDK's default resource). Metrics are pushed every 15 s by a periodic reader. Propagation is W3C Trace Context plus Baggage. Telemetry is started before the router is built (`otelchimetric` binds the global MeterProvider at construction) and flushed last on shutdown, bounded to 5 s. Full table per binary: [Configuration](/docs/operations/configuration).

## Metrics

| Instrument | Type | Unit | Attributes | Meaning |
| --- | --- | --- | --- | --- |
| `shipping.labels.issued` | Int64Counter (meter `github.com/claudioed/shipping-and-tracking`) | `{label}` | `outcome` = `issued` or `rejected`; `reason` (only when rejected) = the problem slug, for example `ship-to-unavailable`, `label-already-issued`, `internal-error` | Every label-issue attempt by outcome. A falling issued share with rising `ship-to-unavailable` means customer-ordering cannot be reached; rising `label-already-issued` means a caller retries without an `Idempotency-Key`; rising `internal-error` means this service is failing |
| HTTP server request duration | Histogram from `otelchi/metric` `NewServerRequestDuration` | per that package | per that package (method, route pattern, status) | One series family per router, on `cmd/api`, `cmd/mcp` and `cmd/a2a` |
| Go runtime metrics | from `go.opentelemetry.io/contrib/instrumentation/runtime` | per that package | none | Goroutines, GC, heap |

Source: `internal/adapters/outbound/telemetry/label_metrics.go` (the counter, recorded by `internal/application/usecases/label_metrics.go`) and `telemetry.go`. This service defines one business instrument of its own; the other two rows come from library instrumentation and their exact names are owned by those libraries. There is no outbox-lag metric: the fleet alert `WarehouseOutboxLagHigh` is not defined for this context in warehouse-infra.

## Traces

| Span | Kind | Created by | Attributes |
| --- | --- | --- | --- |
| `<METHOD> <chi route pattern>` (one name per route, never per tracking number) | server | `otelchi` middleware on every router (`internal/adapters/inbound/http/server.go`, `cmd/mcp/router.go`, `cmd/a2a/router.go`) | otelchi's HTTP attributes |
| `<topic> process` | consumer | `internal/adapters/inbound/kafka/tracing.go`, one per handling attempt | `messaging.system=kafka`, `messaging.destination.name=<topic>`, `messaging.operation.name=process`; errors are recorded on the span |
| `a2a.skill <skill>` | internal | `internal/adapters/inbound/a2a/skills.go` | `a2a.skill` |

Trace context crosses Kafka in both directions. When a use case enqueues an event the active span context is injected into the outbox row's headers inside the same transaction (`internal/adapters/outbound/postgres/outbox_trace.go`), the relay publishes them as ordinary Kafka headers, and a consumer extracts them so its span is a child of the request that enqueued the event. A message without `traceparent` starts a new trace. There are no database or outbound-HTTP client spans.

## Logs

JSON on stdout through `log/slog`. When a record is written with a span-carrying context (the `*Context` variants) the handler adds `trace_id` and `span_id` (`telemetry.WithTraceContext`). Records without a span (boot, shutdown, background loops that log with a plain context) have no trace ids.

| Message | Level | Fields | Source |
| --- | --- | --- | --- |
| `http request` | INFO (4xx WARN, 5xx ERROR) | `method`, `route` (pattern, `unmatched` when none), `status`, `duration_ms`, `bytes`, `request_id`; `/healthz` and `/readyz` are not logged | `internal/adapters/inbound/requestlog/requestlog.go` |
| `request failed` | ERROR | `error`, `method`, `path` | `internal/adapters/inbound/http/response.go` |
| `scheduled job finished` / `scheduled job failed; trying again on the next tick` | INFO / ERROR | `job` (`silence-sweep`, `ship-to-purge`, `housekeeping`), `result` or `error` | `internal/scheduler/scheduler.go` |
| `event processed` | INFO | `type`, `event_id`, `skipped`, `applied` or `recorded` | `internal/adapters/inbound/kafka/handlers.go` |
| `skipping a message that is not a valid CloudEvent`, `skipping an event that can never be applied`, `dead-lettering an event that can never be applied` | WARN | `consumer`, `topic`, `type`, `event_id`, `error` | `internal/adapters/inbound/kafka/consumer.go` |
| `<consumer> handling failed; retrying the same message`, `... after the maximum attempts; dead-lettering the message`, `... broker connection stalled or lost; fetching again after the backoff` | ERROR / WARN | consumer name, attempt data, `error` | `internal/adapters/inbound/kafka/kafka.go` |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause`, `outbox relay pass failed` | ERROR | row id, `error` | `internal/adapters/outbound/outbox/relay.go` |
| `readiness: dependency is not ready` | WARN | `dependency` (`postgres` or `kafka`), `error` | `internal/adapters/inbound/http/readiness.go` |
| `retrying`, `succeeded after retry` | WARN / INFO | `op` (`run migrations`, `ping postgres`), `attempt`, `in`, `err` | `internal/bootretry/bootretry.go` |
| `mcp tool failed with an unexpected error`, `a2a skill failed` | ERROR | `error` | `internal/adapters/inbound/mcp/errors.go`, `a2a/skills.go` |
| `telemetry configured`, `consumer running`, `outbox relay running`, `http server listening` | INFO | `service_name`, `otlp_endpoint`, `consumer`, `topic`, `brokers`, `interval`, `max_attempts`, `addr` | `cmd/api` |

No log line, span attribute, metric attribute or event carries a ship-to address (ADR 0002 A9); `shipto.Address` redacts itself when formatted.

## Dashboards and alerts

Only what exists in warehouse-infra `origin/develop`:

- **Dashboard** `warehouse-shipping-and-tracking` (`terraform/dashboards/contexts/shipping-and-tracking.json`, generated by `scripts/gen-context-dashboards.py`): Kong request rate, 5xx rate and p95 latency; service HTTP RED (rate by route, 5xx by Kong route, p95 by process); the business-metric panel `shipping_labels_issued_total`; Go goroutines and memory; Loki live stream, log volume by level, errors and warnings.
- **Alert rules** (`terraform/alerting/warehouse-alert-rules.json`, groups `warehouse-shipping-and-tracking` and `warehouse-shipping-and-tracking-slo`): `WarehouseKong5xxRatioHigh` (5xx ratio above 5% for the API, MCP and reports routes), `WarehousePodRestarting` (2 or more restarts in 15 minutes of a `shipping-and-tracking-*` pod), `WarehouseApiAvailabilityBurnFast` and `...BurnSlow` (1% error budget), `WarehouseApiLatencyBurnFast` and `...BurnSlow` (400 ms upstream latency target).
- **Not present**: an outbox-lag alert, a consumer-lag alert, alerts on `shipping.labels.issued`.

Both the alerts' `runbook_url` and this repository previously pointed at `docs/operations/runbook.md`; the runbook is now this site's [Runbook](/docs/operations/runbook) page.
