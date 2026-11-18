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
  | `POST /return-labels` | issue the reverse label of a customer return (calls customer-ordering; the destination prefix comes from the local `node_destinations` view when the body names `destinationNodeId`, ADR 0018) |
  | `POST /labels/{labelRef}/voids` | void before handover |
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
- **Migrations**: embedded in the binary and applied with golang-migrate at every boot; its advisory lock makes concurrent starts safe. There is no separate migration job. Two databases, two lists:
  - OLTP database (`internal/adapters/outbound/postgres/migrations/`, applied by `cmd/api`, `cmd/mcp` and `cmd/a2a`): `0001_schema` (eight tables: `shipping_labels`, `ship_to_snapshots`, `package_journeys`, `journey_scans`, `catalog_dimensions`, `outbox_events`, `processed_events`, `idempotency_keys`) and `0002_idempotency_key_global` (the `idempotency_keys` primary key becomes `key` alone, ADR 0010; it first deletes rows that reuse a key on two routes, keeping the oldest) and `0003_journey_eta` (three nullable ETA columns on `package_journeys`, ADR 0013). The later ones follow the same rule, one per ADR; the newest is `0008_node_destinations` (the `node_destinations` read model, ADR 0018), whose table starts EMPTY and needs the rebuild described under [Rebuilding the node destination view](#rebuilding-the-node-destination-view). Use `MIGRATIONS_DATABASE_URL` (direct DSN) when `DATABASE_URL` goes through PgBouncer.
  - Analytical database (`analytics/migrations/`, applied by `cmd/st-projector` through `ANALYTICS_DATABASE_URL`, a direct DSN; `cmd/st-reports` runs no migration): `0001_delivery_facts` (four tables: `analytics_processed_events`, `package_labels`, `journey_scans`, `package_outcomes`). It is a projection of the service's own topic, so it can be dropped and rebuilt by replaying the topic (ADR 0012).

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
| consume | `retailnetwork.node-topology.events` | `ST_AREAS_CONSUMER_GROUP` (`config.areasConsumerGroup`; feeds `area_modes`, `pickup_points` and `node_destinations`) | `retailnetwork.node-topology.events.dlq` |
| consume (`cmd/st-projector`, ADR 0012) | `retailnetwork.shipping-and-tracking.events` (its own topic) | `ANALYTICS_CONSUMER_GROUP` (`shipping-and-tracking-analytics`) | `retailnetwork.shipping-and-tracking.analytics.dlq` (poison only; transient failures retry forever) |

Source: `internal/adapters/inbound/kafka/handlers.go` (topics), `deadletter.go` (`DLQSuffix = ".dlq"`), `internal/adapters/outbound/kafka/encoder.go` (produced topic). Group ids are chart values and must stay stable across restarts so a rescheduled pod resumes from its committed offsets.

## Dependencies

- **Postgres via PgBouncer** — `DATABASE_URL` (pooled) for the runtime pool; `MIGRATIONS_DATABASE_URL` (direct DSN, optional `secretKeyRef`, falls back to `DATABASE_URL`) used only by the boot migrations (`cmd/api/main.go` `openPool`). Schema: `internal/adapters/outbound/postgres/migrations/` (`0001_schema`, `0002_idempotency_key_global`). The analytical database has its own schema in `analytics/migrations/` and its own DSNs (see Migrations above).
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

### Rebuilding the node destination view

`node_destinations` (ADR 0018) holds, per node of node-topology, the destination prefix of a return label that names its destination by `destinationNodeId`. It is fed by `NodeRegistered`, which node-topology emits ONCE per node. Migration `0008` creates the table empty, and every `NodeRegistered` consumed before it fed only `pickup_points`, so every node registered before the deploy is unknown to the view until it is rebuilt. This repository has no replay tooling: the rebuild is a change of consumer group.

**What the inbox does to a redelivered id.** A claim is the pair `(consumer, CloudEvents id)` in `processed_events`, written in the transaction of the effect and kept 30 days. The older folds of the node-topology topic (`area_modes`, `pickup_points`) claim as `node-topology`; the `node_destinations` fold claims as `node-topology.node-destinations`. On a replay of the topic:

- The `node_destinations` fold has never claimed those ids, so each `NodeRegistered` is applied once; a second delivery of the same id is skipped (`TestNoteNodeDestinationSkipsARedeliveredEventID`).
- The older folds skip every id they claimed within the last 30 days: `area_modes` and `pickup_points` do not change (`TestAReplayedNodeRegisteredFillsTheNodeViewAndLeavesThePickupPointAlone`, both in `internal/application/usecases/node_destinations_test.go`).
- An id older than 30 days is no longer claimed and IS applied again by the older folds. `area_modes` drops it (the assignment-version guard). `pickup_points` applies a replayed `NodeRegistered` as a registration applied again: station and prefix are written with the same values and the state is kept, so a point is not demoted to `PROPOSED` (ADR 0016, addendum 2026-11-18). A replayed status fact sets the state it names, so a point walks through its recorded history and ends in its current state (see [Repairing the pickup point view](#repairing-the-pickup-point-view) for what that means for a label). This arises only if the topic retains more than 30 days.
- A message that was dead-lettered the first time is dead-lettered again: expect duplicates on `retailnetwork.node-topology.events.dlq`.

**The dependency.** The replay only finds what the topic still retains. A node whose `NodeRegistered` is past the topic's retention cannot be restored this way and stays `409 destination-prefix-unavailable`. This service has no other source: it never calls node-topology, and node-topology does not publish a node's registration a second time.

**Procedure**, to be finished before any caller sends `destinationNodeId`:

1. Deploy the version that carries migration `0008`.
2. For every node id a caller will send, confirm that the topic still holds its registration: `kafka-console-consumer.sh --bootstrap-server "$KAFKA_BROKERS" --topic retailnetwork.node-topology.events --from-beginning --timeout-ms 30000 | grep NodeRegistered | grep '"nodeId":"<node id>"'`. No line for a node means step 5 will not find it either: stop and escalate, do not enable the caller for that node.
3. Set `config.areasConsumerGroup` (`ST_AREAS_CONSUMER_GROUP`) to a group id that has never existed, for example `shipping-and-tracking-areas-r2`, wherever the deployment sets it, and roll out. A group with no committed offset starts at the first retained offset (kafka-go's default `StartOffset`, which `readerConfig` in `internal/adapters/inbound/kafka/kafka.go` leaves unset), so the consumer reads the whole topic again.
4. Wait until the new group has no lag: `kafka-consumer-groups.sh --bootstrap-server "$KAFKA_BROKERS" --describe --group shipping-and-tracking-areas-r2`.
5. Verify the view: `SELECT node_id, postal_prefix, updated_at FROM node_destinations WHERE node_id IN (...);`. Every node a caller will send must have a row with a non-empty `postal_prefix`. An empty prefix means the node's registered postal code has no letter or digit from A-Z 0-9 and must be corrected in node-topology.
6. Keep the new group id; it is now the stable one. (Going back to the old id is harmless, its committed offsets resume and every id in between is already claimed, but it gains nothing.)

A caller enabled earlier is refused, not stuck: a request for a node the view does not hold answers `409 destination-prefix-unavailable`, and that refusal is not stored under the caller's `Idempotency-Key` (ADR 0018). The caller retries the same request with the same key; it is executed again each time and is served as soon as the node's row exists. Until then no return label can be issued for that node, which is why the rebuild comes first.

### Repairing the pickup point view

`pickup_points` (ADR 0016) holds node-topology's pickup points: serving station, prefix and state. Every version before the fix of 2026-11-18 folded a status fact (`NodeActivated`, `NodeSuspended`, `NodeRetired`) as a whole row, so a database that ran one of them is damaged in three ways:

- every pickup point that received a status fact has `station_node_id = ''` and `postal_prefix = ''`. A label for it was never issued: `POST /labels` answered 422 `validation-failed` with the detail `destinationPostalPrefix must be 1 to 16 characters from A-Z 0-9`;
- every node of another type that received a status fact has a row of its own, with both columns empty;
- a retired point has no row (it was deleted; it is now kept as `RETIRED`).

The fix stops the damage and repairs nothing: station and prefix come from `NodeRegistered` alone, which node-topology emits ONCE per node, and this service has no other source. No migration is involved. This repository has no replay tooling: the repair is the registrations applied again.

**What the inbox does to a redelivered id.** The pickup-point fold claims every node fact as `(node-topology, CloudEvents id)` in `processed_events`, the identity it shares with the `area_modes` fold; claims are kept 30 days. On a replay of the topic under a new consumer group:

- a `NodeRegistered` whose claim still stands is SKIPPED, and the blank row stays blank. A new consumer group alone repairs only the points whose registration was consumed more than 30 days ago;
- once the claims of that identity are cleared, the replayed `NodeRegistered` restores station and prefix and keeps the point's state.

Both halves are `TestAReplayedRegistrationRepairsABlankedRowOnlyOnceItsClaimIsCleared` (`internal/adapters/inbound/kafka/handlers_pickup_points_test.go`). So the claims of `node-topology` have to be cleared. What that does to the other folds of the topic: `area_modes` applies each replayed `ServiceAreaAssigned` again and drops it under the assignment-version guard; `node_destinations` claims under its own identity `node-topology.node-destinations`, which is NOT cleared, so it skips what it claimed (and rewrites the same prefix for an id older than 30 days).

**What the replay does to a point's state.** The status facts are replayed too, in their original order. A point that is `ACTIVE` now and was suspended once is `SUSPENDED` between its replayed `NodeSuspended` and the `NodeActivated` that followed. A point retired before the fix has no row: its replayed registration inserts it `PROPOSED`, its `NodeActivated` makes it `ACTIVE` and its `NodeRetired` makes it `RETIRED` for good. All facts of a node are on one partition and are replayed back to back, so each of these moments is short, but a pickup label asked in one of them gets the answer of that moment. A refusal in such a moment does no lasting harm: 422 `pickup-point-unavailable` is not stored under the caller's `Idempotency-Key`, so the caller's retry with the same key is executed again and served. A label issued to a retired point in the moment it is replayed as `ACTIVE` is a real label, though. Run the repair while no pickup labels are being requested.

**The dependency.** The replay only finds what the topic still retains. A point whose `NodeRegistered` is past the topic's retention cannot be repaired this way: its row stays blank (or absent) and its labels stay refused. This service never calls node-topology, and node-topology does not publish a registration a second time.

**Procedure**

1. Deploy the version that carries the fix (no migration).
2. List the damage: `SELECT id, state, updated_at FROM pickup_points WHERE postal_prefix = '' ORDER BY id;`. For every pickup point among those ids, and for every pickup point that has no row at all, confirm that the topic still holds its registration: `kafka-console-consumer.sh --bootstrap-server "$KAFKA_BROKERS" --topic retailnetwork.node-topology.events --from-beginning --timeout-ms 30000 | grep NodeRegistered | grep '"nodeId":"<node id>"'`. The line also tells the node's type: an id whose `nodeType` is not `PICKUP_POINT` is one of the rows that should never have existed. No line for a pickup point means the replay will not repair it: escalate to node-topology's owners, who hold its station and postal code.
3. Clear the claims of the pickup-point fold: `DELETE FROM processed_events WHERE consumer = 'node-topology';`.
4. Set `config.areasConsumerGroup` (`ST_AREAS_CONSUMER_GROUP`) to a group id that has never existed, for example `shipping-and-tracking-areas-r3`, and roll out. A group with no committed offset starts at the first retained offset, so the consumer reads the whole topic again.
5. Wait until the new group has no lag: `kafka-consumer-groups.sh --bootstrap-server "$KAFKA_BROKERS" --describe --group shipping-and-tracking-areas-r3`.
6. Verify: `SELECT id, station_node_id, postal_prefix, state FROM pickup_points ORDER BY id;`. Every pickup point has a non-empty `postal_prefix`, and its state is the one node-topology reports for it.
7. Remove the rows of nodes that are not pickup points: `DELETE FROM pickup_points WHERE postal_prefix = '';`, after checking each remaining id against step 2. A registration always carries a postal code, so after the replay an empty prefix is a node of another type, or a pickup point whose registration the topic no longer holds (the escalation of step 2: deleting that row changes its refusal from 422 `validation-failed` to 422 `pickup-point-unavailable`, nothing else). The fixed fold never writes such a row again.
8. Keep the new group id; it is now the stable one.

A restored prefix is always usable by a label: node-topology refuses to register a `PICKUP_POINT` whose postal code is not wholly A-Z 0-9, so a code with a separator such as `07750-000` belongs to a node of another type, which this view does not keep (ADR 0016, addendum 2026-11-18).
