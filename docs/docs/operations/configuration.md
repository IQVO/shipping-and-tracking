---
id: configuration
title: Configuration
sidebar_label: Configuration
description: "Every environment variable read by cmd/api, cmd/mcp and cmd/a2a: default, required or not, meaning, and the chart value that sets it."
---

# Configuration

Shipping & Tracking ships three binaries from one image and one Go module. Each reads its own environment; a variable read by several binaries appears in each table with that binary's own default. All three log JSON through `log/slog` on stdout and refuse to boot on a missing mandatory variable with an error that **names the variable and never prints a secret value**.

Source: `cmd/api/config.go`, `cmd/api/telemetry.go`, `cmd/api/main.go` (header comment), `cmd/mcp/main.go`, `cmd/mcp/telemetry.go`, `cmd/a2a/main.go`, `cmd/a2a/telemetry.go`. The Helm values that render each variable are in `charts/shipping-and-tracking/values.yaml`; `charts/shipping-and-tracking/tests/render_test.sh` asserts that every variable `cmd/api` reads is rendered.

Durations (marked *duration*) accept a Go duration (`750ms`, `5m`) or plain seconds (`1.5`) unless a row says otherwise. An invalid or non-positive value falls back to the default with a `WARN` log, except `CUSTOMER_ORDERING_TIMEOUT`, which is a boot error.

## `cmd/api` (REST, outbox relay, Kafka consumers, jobs)

Default listen address `:8080`. This is the only binary that dials Kafka.

| Variable | Default | Required | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | HTTP listen address of the REST API and of `/healthz`, `/readyz`. | `config.httpAddr` |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the runtime pool (may point at PgBouncer). Boot error `DATABASE_URL is required` when empty: there is no in-memory mode. | Secret `database.existingSecret` / key `database.existingSecretKey` |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only by the boot migrations, because golang-migrate's advisory lock is session-scoped and PgBouncer cannot honour it. | `database.migrationsExistingSecretKey` (optional `secretKeyRef`) |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list for the relay, the four consumers and the `/readyz` broker check. Boot error when empty. | `kafka.brokers` |
| `EVENT_PUBLISHER` | `kafka` | no | Only `kafka` (or empty) is accepted. `log` is a boot error (`not supported`), any other value is a boot error (`unknown value`). The variable survives because the deployment sets it. | `config.eventPublisher` |
| `CUSTOMER_ORDERING_URL` | none | **yes** | Absolute http(s) base URL of customer-ordering for `GET {base}/orders/{orderId}/ship-to`. The gateway constructor refuses a non-absolute base. | `config.customerOrderingUrl` |
| `CUSTOMER_ORDERING_TIMEOUT` | `3s` | no | Bound of one GetShipTo call (*duration*). Empty uses the default; malformed or non-positive is a boot error. | `config.customerOrderingTimeout` |
| `SHIP_TO_ENCRYPTION_KEY` | none | **yes** | Base64 of the 32-byte AES-256-GCM key that seals the ship-to snapshot. Always on, there is no clear-text mode. A key that does not decode to 32 bytes is a boot error. | Secret `crypto.existingSecret` / `crypto.shipToEncryptionKeyKey` |
| `STOP_KEY_SECRET` | none | **yes** | HMAC-SHA-256 secret of the `stopKey` (the keyed hash of the delivery stop). At least 16 bytes; shorter is a boot error. | Secret `crypto.existingSecret` / `crypto.stopKeySecretKey` |
| `VENDOR_CONSUMER_GROUP` | none | **yes** | Consumer group id of the consumer of `retailnetwork.vendor-connectivity.events`. Stable, service-prefixed, never the pod name. | `config.vendorConsumerGroup` |
| `LINEHAUL_CONSUMER_GROUP` | none | **yes** | Group id for `retailnetwork.linehaul-transportation.events`. | `config.linehaulConsumerGroup` |
| `LASTMILE_CONSUMER_GROUP` | none | **yes** | Group id for `retailnetwork.last-mile-delivery.events`. | `config.lastmileConsumerGroup` |
| `CATALOG_CONSUMER_GROUP` | none | **yes** | Group id for `retailnetwork.offer-availability.events`. | `config.catalogConsumerGroup` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Poll interval of the outbox relay (*duration*). | `config.outboxRelayInterval` |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Publish failures before the relay excludes a row from future claims; `0` means unlimited. A negative or non-numeric value logs a `WARN` and uses `20` so a typo never silently removes the cap. | `config.outboxMaxAttempts` |
| `SILENCE_SWEEP_INTERVAL` | `5m` | no | Period of the `silence-sweep` job (*duration*); the job also runs once at start. | `config.silenceSweepInterval` |
| `HOUSEKEEPING_INTERVAL` | `1h` | no | Period of both the `ship-to-purge` and the `housekeeping` jobs (*duration*); both also run at start. | `config.housekeepingInterval` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait after `/readyz` flips to 503 before the HTTP server drains. Go duration only (not plain seconds); `0` disables; negative or unparsable logs a `WARN` and uses `5s`. The chart takes whole seconds only. | `config.shutdownDrainDelay` |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive; anything else is `info`. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `shipping-and-tracking` | no | `service.name` on spans and metrics. | `otel.serviceName` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, `host:port` or URL; a bare `host:port` becomes `http://host:port`. Export never blocks the service. | `otel.endpoint` (chart default `otel-collector.observability.svc.cluster.local:4317`) |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. | image tag (`appVersion` when empty) |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. | `environment` (chart default `production`) |

The chart sets `terminationGracePeriodSeconds: 45` and refuses any value not greater than 30 s plus `config.shutdownDrainDelay`: the shutdown bounds are drain delay + HTTP 10 s + consumers and jobs 10 s + relay 10 s (`cmd/api/main.go`).

## `cmd/mcp` (read-only MCP server)

Default listen address `:8090`, Streamable HTTP only. It runs the embedded migrations at boot (idempotent, advisory-locked) but dials no Kafka and reads none of the Kafka, ship-to, secret or consumer-group variables.

| Variable | Default | Required | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address. | `mcp.httpAddr` |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the database `cmd/api` owns. Boot error `DATABASE_URL is required` when empty. | same Secret as the api |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations only. | same Secret as the api |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning`, `error`. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `shipping-and-tracking-mcp` | no | `service.name`. | `otel.serviceName` + `-mcp` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. | `otel.endpoint` |
| `SERVICE_VERSION` | `dev` | no | `service.version`, also printed in the boot line. | `mcp.serviceVersion` (image tag when empty) |
| `ENVIRONMENT` | `development` in the boot log line, `local` on the telemetry resource | no | `deployment.environment.name`. The two defaults differ because the boot line reads the variable with its own fallback (`cmd/mcp/main.go`) while `telemetry.Setup` uses `DefaultEnvironment`. | `mcp.environment` (chart default `production`) |

## `cmd/a2a` (read-only A2A agent)

Default listen address `:8093`. Like `cmd/mcp` it dials no Kafka. A missing or malformed mandatory variable is reported, all problems together (`errors.Join`), before telemetry, any connection and the listener.

| Variable | Default | Required | Meaning | Chart value |
| --- | --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address. | `a2a.httpAddr` |
| `A2A_AGENT_URL` | none | **yes** | Absolute http(s) URL (with a host) of this agent's JSON-RPC endpoint, advertised on the Agent Card. Boot error otherwise. | `a2a.agentUrl`; when empty the chart renders `http://<fullname>-a2a.<namespace>.svc.cluster.local:<service.port>/` |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the database `cmd/api` owns. | same Secret as the api |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations only. | same Secret as the api |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`/`warning`, `error`. | `config.logLevel` |
| `OTEL_SERVICE_NAME` | `shipping-and-tracking-a2a` | no | `service.name`. | `otel.serviceName` + `-a2a` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint. | `otel.endpoint` |
| `SERVICE_VERSION` | `dev` | no | `service.version`, boot line and the version on the Agent Card. | `a2a.serviceVersion` (image tag when empty) |
| `ENVIRONMENT` | `development` in the boot log line, `local` on the telemetry resource | no | `deployment.environment.name` (same two fallbacks as `cmd/mcp`). | `a2a.environment` (chart default `production`) |

## Notes

- `web/` (the console remote) renders no environment at all in the cluster (`frontend.enabled` in `values.yaml`), so it has no table here.
- The chart refuses `EVENT_PUBLISHER` other than `kafka`, any `*_MODE` variable and any variable it renders itself when they arrive through `extraEnv` (`values.yaml` comments, enforced in `render_test.sh`).
- There is no authentication variable: REST, MCP and A2A are unauthenticated and the access boundary is the in-cluster Service.
- Test-only variables (the Docker and container helpers behind `make integration`) are not listed.
