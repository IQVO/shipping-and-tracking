---
id: runbook
title: Runbook
sidebar_label: Runbook
description: "Deploying, probing and operating Shipping & Tracking: Helm chart, readiness, migrations, Kafka topics and consumer groups, outbox relay, DLQ, sweepers and routine procedures."
---

# Shipping & Tracking runbook

Issues the shipping label of each package (the only place outside customer-ordering that ever sees a ship-to address — stored only as an AES-256-GCM snapshot) and owns every package's journey: one customer-visible timeline consolidated from vendor, linehaul and last-mile facts, ending in delivered, returned or lost. Domain-driven design study: no real company, brand or dataset — all names and figures are synthetic (see the disclaimer in `README.md`). Contracts: `apis/openapi.yaml`, `apis/asyncapi.yaml`; decisions: `docs/adr/`.

## Runtime shape

One `api` Deployment, one process (`cmd/api/main.go`, the composition root — there is no in-memory mode, no log sink, no worker that is off):

- REST on `HTTP_ADDR` (default `:8080`), chi router (`internal/adapters/inbound/http/server.go`):

  | Route | Use case |
  | --- | --- |
  | `POST /labels` | issue a label (calls customer-ordering, seals the snapshot, starts the journey) |
  | `POST /labels/{labelRef}:void` | void before handover |
  | `GET /journeys/{trackingNumber}` | the timeline |
  | `POST /journeys/{trackingNumber}/scans` | record a scan |

- The transactional-outbox relay (`internal/adapters/outbound/outbox/relay.go`): polls `outbox_events` every `OUTBOX_RELAY_INTERVAL` and publishes to Kafka topic `retailnetwork.shipping-and-tracking.events` (`internal/adapters/outbound/kafka/encoder.go`); each row is one pre-encoded CloudEvents message inserted in the SAME transaction as the aggregate.
- Four consumers (`internal/adapters/inbound/kafka/handlers.go`): `retailnetwork.vendor-connectivity.events`, `retailnetwork.linehaul-transportation.events`, `retailnetwork.last-mile-delivery.events`, `retailnetwork.offer-availability.events`. A transient handler failure retries the same message with capped exponential backoff, then dead-letters to `<topic>.dlq` (`consumer.go`, `deadletter.go`); a message that is not a valid CloudEvent, or of a type the consumer does not act on, is skipped with a WARN instead.
- Three scheduler jobs (`cmd/api/main.go` `startJobs`, `internal/scheduler/scheduler.go`; every tick logs `scheduled job finished` with `job` and `result`, a failed tick is logged and retried on the next tick):
  - `silence-sweep` (`SILENCE_SWEEP_INTERVAL`, default 5m, runs on start): the ExceptionOnSilence policy — a watched journey silent past its tier/state window (6h same-day, 24h next-day, 48h two-day, 72h standard, 12h out-for-delivery; `internal/domain/journey/types.go`) gets a `NO_SCAN` exception raised; `PackageLost` 48h after an unanswered exception on an in-transit journey. Log line: `raised=<n> lost=<n> conflicts=<n>`.
  - `ship-to-purge` and `housekeeping` (both `HOUSEKEEPING_INTERVAL`, default 1h, both run on start): `ship-to-purge` deletes `ship_to_snapshots` 30 days after the journey closed (ADR 0002 A9); `housekeeping` purges the technical tables per ADR 0005 — `idempotency_keys` after 24h, already-relayed `outbox_events` rows after 7d, `processed_events` (inbox) after 30d (`cmd/api/config.go` retention consts).

Two optional read-only deployables run beside it, both off by default: `cmd/mcp` (ADR 0006) and `cmd/a2a` (ADR 0009, the A2A agent: `get_journey` over JSON-RPC at `/` and `/a2a`, Agent Card at `/.well-known/agent-card.json`, `A2A_ADDR` default `:8093`). Neither dials Kafka nor runs the relay, the consumers or the jobs; `cmd/a2a` refuses to boot without `DATABASE_URL` and `A2A_AGENT_URL`, and with `a2a.enabled=true` the chart defaults `A2A_AGENT_URL` to the component's in-cluster Service URL (override with `a2a.agentUrl`).

Any consumer, job loop or the relay returning on its own makes the process exit non-zero so the orchestrator restarts it (`cmd/api/main.go` `watchWorkers`; a worker death logs `consumer stopped` / `outbox relay stopped`). Shutdown order: `/readyz` flips 503 → wait `SHUTDOWN_DRAIN_DELAY` (5s) → HTTP drain (≤10s) → consumers+jobs (≤10s) → the relay LAST so nothing committed is stranded (≤10s); the chart refuses a `terminationGracePeriodSeconds` not greater than 30s + the delay (45s default).

## Deployment

- **Image**: one image (`Dockerfile`) carries every `cmd/*` binary in `/app/` (`api`, `mcp`, `a2a`); `ENTRYPOINT ["./api"]`, exposes 8080, 8090 and 8093, runs as uid 1000, writes nothing to disk. Published to `ghcr.io/iqvo/shipping-and-tracking` by the `docker-publish` job on push to `main` (`.github/workflows/ci.yml`).
- **Chart**: `charts/shipping-and-tracking/`, packaged as `shipping-and-tracking-<version>.tgz` by the `release` job and pushed to `oci://ghcr.io/iqvo`. The default component is the `api` Deployment (Service port 80 to 8080). Optional components, all off by default: `mcp.enabled` (command `/app/mcp`, port 8090), `a2a.enabled` (command `/app/a2a`, port 8093) and `frontend.enabled` (the nginx pod serving the `web/` console remote, reached through the web gateway, never routed by this chart). Kong routes for MCP and A2A are `mcp.route.enabled` (`/api/mcp/shipping-and-tracking`) and `a2a.route.enabled` (`/api/a2a/shipping-and-tracking`).
- **Required values**: the chart refuses to render without `database.url` or `database.existingSecret`, without `kafka.brokers`, `config.customerOrderingUrl` and the four `config.*ConsumerGroup` values, without the crypto Secret (`crypto.existingSecret` or both inline values), and with a `terminationGracePeriodSeconds` not greater than 30 s plus `config.shutdownDrainDelay` (`values.yaml`, `tests/render_test.sh`).
- **Probes** (api): `startupProbe` and `livenessProbe` on `/healthz`, `readinessProbe` on `/readyz`. `cmd/mcp` and `cmd/a2a` probe `/healthz` for all three (they have no readiness dependency check).
- **Scaling**: `replicaCount: 1` by default; `autoscaling.api.enabled` adds an HPA (1 to 4 replicas at 70% CPU). More than one api replica is safe by design (the relay claims rows with `SKIP LOCKED`, consumer groups are stable, the inbox dedupes inside the handler transaction) but has not been load-tested.
- **Migrations**: embedded in the binary (`internal/adapters/outbound/postgres/migrations/0001_schema.up.sql` and `.down.sql`) and applied by `cmd/api`, `cmd/mcp` and `cmd/a2a` at every boot with golang-migrate; its advisory lock makes concurrent starts safe. There is no separate migration job. Use `MIGRATIONS_DATABASE_URL` (direct DSN) when `DATABASE_URL` goes through PgBouncer.

### What readiness waits for

`/readyz` (api only) is 200 when the cached verdict (3 s TTL, 2 s bound per round) says both Postgres (`pool.Ping`) and Kafka (a metadata request against `KAFKA_BROKERS`) answer. It does not wait for the consumers to have joined their groups or for the relay to be caught up. During shutdown it returns 503 `{"status":"not_ready"}` for `SHUTDOWN_DRAIN_DELAY` before the listener drains.

### Kafka topics and consumer groups

| Direction | Topic | Group (env var) | Dead-letter topic |
| --- | --- | --- | --- |
| produce | `retailnetwork.shipping-and-tracking.events` | none | none |
| consume | `retailnetwork.vendor-connectivity.events` | `VENDOR_CONSUMER_GROUP` | `retailnetwork.vendor-connectivity.events.dlq` |
| consume | `retailnetwork.linehaul-transportation.events` | `LINEHAUL_CONSUMER_GROUP` | `retailnetwork.linehaul-transportation.events.dlq` |
| consume | `retailnetwork.last-mile-delivery.events` | `LASTMILE_CONSUMER_GROUP` | `retailnetwork.last-mile-delivery.events.dlq` |
| consume | `retailnetwork.offer-availability.events` | `CATALOG_CONSUMER_GROUP` | `retailnetwork.offer-availability.events.dlq` |

Source: `internal/adapters/inbound/kafka/handlers.go` (topics), `deadletter.go` (`DLQSuffix = ".dlq"`), `internal/adapters/outbound/kafka/encoder.go` (produced topic). Group ids are chart values and must stay stable across restarts so a rescheduled pod resumes from its committed offsets.

## Dependencies

- **Postgres via PgBouncer** — `DATABASE_URL` (pooled) for the runtime pool; `MIGRATIONS_DATABASE_URL` (direct DSN, optional `secretKeyRef`, falls back to `DATABASE_URL`) used only by the boot migrations (`cmd/api/main.go` `openPool`). Schema: `internal/adapters/outbound/postgres/migrations/0001_schema.up.sql`.
- **Kafka** — `KAFKA_BROKERS`, comma-separated; dialed lazily on first send/read, so a broker outage never blocks boot. Topics are auto-created by the first writer (no manual creation step).
- **customer-ordering REST** — `CUSTOMER_ORDERING_URL`; the model's one synchronous call, `GET {base}/orders/{orderId}/ship-to` (`internal/adapters/outbound/customerordering/`), made before the label transaction opens. 404 → `ports.ErrNotFound`; any other failure → `ports.ErrShipToUnavailable`.
- **Secrets** (local cluster): `shipping-and-tracking-db` (`DATABASE_URL`, `MIGRATIONS_DATABASE_URL`) and `shipping-and-tracking-crypto` (`SHIP_TO_ENCRYPTION_KEY`, `STOP_KEY_SECRET`). Chart keys: `database.existingSecret`, `crypto.existingSecret`.

## Health

- `GET /healthz` — liveness only; never flipped by shutdown or by a dependency (`internal/adapters/inbound/http/readiness.go`). Used by the startup and liveness probes.
- `GET /readyz` — 200 only while a Postgres ping AND a Kafka broker metadata request succeed; the verdict is cached 3s and one round of checks is bounded 2s. 503 `{"status":"not_ready"}` while draining; 503 problem+json `dependency-unavailable` naming the failed dependency (`postgres` or `kafka`). Used by the readiness probe.
- Writes are NOT gated on readiness: a `POST /labels` commits label + encrypted snapshot + outbox row in ONE transaction; only the relay ever touches Kafka. So with **Kafka down, REST writes are still accepted (201 for `POST /labels`, 200 for the others) and queue in `outbox_events`** (verified by `cmd/api/e2e_integration_test.go`: Kafka removed → `/readyz` goes 503, `/healthz` stays 200). With **Postgres down, REST writes fail** (500 `internal-error`).

## Configuration

Every variable `cmd/api` reads (`cmd/api/config.go`, header of `cmd/api/main.go`) and the chart value that sets it (`charts/shipping-and-tracking/values.yaml`, rendered by `templates/deployment.yaml` + `templates/configmap.yaml`; `tests/render_test.sh` asserts the mapping is total):

| Env var | Chart value | Default / notes |
| --- | --- | --- |
| `HTTP_ADDR` | `config.httpAddr` | `:8080` |
| `LOG_LEVEL` | `config.logLevel` | `info` (debug\|warn\|error) |
| `OTEL_SERVICE_NAME` | `otel.serviceName` | `shipping-and-tracking` (`-mcp` / `-a2a` suffix for the mcp and a2a components); ADR 0008 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `otel.endpoint` | `localhost:4317` without the chart; export never blocks |
| `SERVICE_VERSION` / `ENVIRONMENT` | `image.tag` (appVersion when empty) / `environment` | `dev` / `local` without the chart |
| `EVENT_PUBLISHER` | `config.eventPublisher` | `kafka` only; `log` is refused at render and at boot |
| `KAFKA_BROKERS` | `kafka.brokers` | REQUIRED |
| `CUSTOMER_ORDERING_URL` | `config.customerOrderingUrl` | REQUIRED, absolute http(s) URL |
| `CUSTOMER_ORDERING_TIMEOUT` | `config.customerOrderingTimeout` | 3s; bounds one GetShipTo call |
| `SHIP_TO_ENCRYPTION_KEY` | `crypto.existingSecret` / `crypto.shipToEncryptionKey` | REQUIRED, base64 of 32 bytes |
| `STOP_KEY_SECRET` | `crypto.existingSecret` / `crypto.stopKeySecret` | REQUIRED, ≥16 bytes; HMAC-SHA-256 of the stopKey |
| `DATABASE_URL` | `database.existingSecret` (`database.existingSecretKey`) | REQUIRED, pooled DSN |
| `MIGRATIONS_DATABASE_URL` | `database.migrationsExistingSecretKey` | optional, direct DSN; falls back to `DATABASE_URL` |
| `VENDOR_CONSUMER_GROUP` | `config.vendorConsumerGroup` | REQUIRED; likewise `LINEHAUL_`, `LASTMILE_`, `CATALOG_CONSUMER_GROUP` from `config.linehaulConsumerGroup`, `config.lastmileConsumerGroup`, `config.catalogConsumerGroup` — stable service-prefixed ids, never the pod name |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | 1s |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | 20; `0` = unlimited |
| `SILENCE_SWEEP_INTERVAL` | `config.silenceSweepInterval` | 5m |
| `HOUSEKEEPING_INTERVAL` | `config.housekeepingInterval` | 1h (ship-to purge + technical-table purge) |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | 5s; `0` disables; must fit the pod grace period |

## Alerts and dashboards

- Grafana dashboard uid `warehouse-shipping-and-tracking`; pod-restart alerts exist for this Deployment.
- One business metric is instrumented (ADR 0008): `shipping.labels.issued` by `outcome` (`issued`/`rejected`) and `reason`. A rising `ship-to-unavailable` means customer-ordering cannot be reached; a rising `internal-error` means this service is failing. Dashboards come from warehouse-infra.

## Failure modes

### Pod crash-loops at boot

- **Symptom**: `CrashLoopBackOff`; the container exits 1 immediately, before binding.
- **Diagnosis**: `kubectl -n warehouse-systems logs deploy/shipping-and-tracking --previous` — the exit message names the missing variable, never the secret value (`cmd/api/config.go` `loadConfig` validates everything before anything is opened or bound).
- **Likely causes**: missing/invalid `SHIP_TO_ENCRYPTION_KEY` (must be base64 of 32 bytes) or `STOP_KEY_SECRET` from Secret `shipping-and-tracking-crypto` — the ship-to snapshot is ALWAYS encrypted, there is no clear-text mode; `EVENT_PUBLISHER=log`; an empty consumer-group var; a non-absolute `CUSTOMER_ORDERING_URL`; missing `DATABASE_URL`/`KAFKA_BROKERS`. Slow Postgres also means a slow start: migrations and the first ping retry with bounded backoff (`internal/bootretry`).
- **Action**: fix the Secret/ConfigMap value, then the next restart succeeds.

### Postgres unreachable

- **Symptom**: `/readyz` 503 naming `postgres`; REST writes fail; relay, consumers and jobs log errors.
- **Diagnosis**: check PgBouncer/Postgres ahead of the service.
- **Action**: none inside this service — every loop retries; recovery is automatic once the database answers. If migrations can never run, the pod never becomes ready.

### Kafka unreachable

- **Symptom**: `/readyz` 503 naming `kafka`; `/healthz` stays 200; `outbox_events` backlog grows.
- **Diagnosis**: confirmed Kafka outage (broker list in `KAFKA_BROKERS`).
- **Action**: none — REST writes are still accepted and queued in `outbox_events`; on recovery the relay drains the backlog in `id` order. No committed event is lost while Postgres is up (`cmd/api/e2e_integration_test.go`).

### Outbox relay row parked after max attempts

- **Symptom**: log line `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` (`internal/adapters/outbound/outbox/relay.go`).
- **Diagnosis**: `SELECT id, event_id, topic, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;` (the cap is `OUTBOX_MAX_ATTEMPTS`, default 20).
- **Action**: after fixing the cause (usually Kafka), re-arm the row: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. The relay republishes the same `event_id` (column `outbox_events.event_id`, minted once at encode time), so consumers stay idempotent.

### Consumer lag or retry logs

- **Symptom**: repeated retry/fetch-error logs for one consumer; group lag grows.
- **Diagnosis**: an offset commits only after its handler returns nil — a failing message retries in place with capped backoff, then dead-letters to `<topic>.dlq`. A consumed topic whose producer has not written yet only logs reader retries: topics are auto-created by the first writer, so a silent vendor/linehaul/last-mile/catalog feed is an upstream gap, not a fault of this service.
- **Action**: inspect `<topic>.dlq` for poison messages; check the producing context before touching this one.

### customer-ordering REST unreachable

- **Symptom**: `POST /labels` answers 503 `ship-to-unavailable` (`internal/adapters/inbound/http/errors.go` maps `ports.ErrShipToUnavailable`).
- **Diagnosis**: customer-ordering down or slow; the call is bounded by `CUSTOMER_ORDERING_TIMEOUT` (3s) and precedes the transaction, so it never holds a lock.
- **Action**: only label issuing degrades — void, scans, queries and all consumers work; recovery is automatic, and retrying the same `Idempotency-Key` is safe.

## Operational actions

- Restart: `kubectl -n warehouse-systems rollout restart deploy/shipping-and-tracking`; history/image: `kubectl -n warehouse-systems rollout history deploy/shipping-and-tracking`.
- Logs (Grafana Loki, label `app=shipping-and-tracking`): JSON slog on stdout. Boot errors name the missing variable; job ticks log `scheduled job finished` with `job`/`result`; worker deaths log `consumer stopped` / `outbox relay stopped` immediately before the non-zero exit.
- Crypto key rotation: every snapshot stores its sealing key's id in `ship_to_snapshots.key_id` (first 8 hex chars of the key's SHA-256, `internal/adapters/outbound/crypto/crypto.go`) precisely so a rotation can tell which key sealed which row; opening with a different key fails (`ErrBadKey`). No production path reads a snapshot back (write-only; purged 30d after the journey closes — the migration comment says "never read back by a response"), so rotating `SHIP_TO_ENCRYPTION_KEY` breaks no live read path — but pre-rotation snapshots become unopenable by the new key. Rotating `STOP_KEY_SECRET` changes the `stop_key`/postal prefix derived for NEW labels; existing `shipping_labels` rows keep theirs.
