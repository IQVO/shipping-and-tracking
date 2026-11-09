# ADR 0012: analytics read side

## Status

Accepted (2026-11-08). Additive: no published payload, topic, OLTP table or REST route of the api changes. Adds two
binaries (`cmd/st-projector`, `cmd/st-reports`), one analytical-database migration set (`analytics/migrations`), three
read-only report routes, two default-off chart components and a second, separate database.

## Context

Shipping & Tracking owns the one customer-visible timeline of every package (ADR 0001, ADR 0002), so it is the only
place that can answer two network questions as a trend: how many times a package is handled before it reaches the
customer, and how often the first delivery attempt succeeds. Nothing queries that today: the facts leave as CloudEvents
and the OLTP tables hold the current state only, with no history in a shape a report can aggregate.

No retail-network context has an analytics read side yet; the WMS fleet does (a separate analytical Postgres fed by a
projector, a read-only reports binary over it). The closest reference is network-inventory-planning (ADR 0009 there):
its publisher already existed and only the consuming half was built. This context is in the same position with one
difference: every event it publishes is an integration event (ADR 0004; contracts-delta E6 and E16 promoted the
analytics-only ones), so **nothing is published on `retailnetwork.shipping-and-tracking.analytics`** and every fact the
reports need is already on `retailnetwork.shipping-and-tracking.events`. This ADR is the pilot: last-mile-delivery,
delivery-promise and order-sourcing copy its shape.

## Decision

### 1. What is projected

The projector consumes ONLY this context's own integration topic, `retailnetwork.shipping-and-tracking.events`. It
never reads a sibling's topic (the attempt outcomes last-mile-delivery publishes are NOT read: the journey already
records every attempt as a scan of its own). The analytics topic is not subscribed while nothing is published on it; a
future analytics-only event adds it to the projector in the ADR that introduces the event. No event is fanned out
(`FanoutEncoder` is not needed) and the outbox is unchanged.

Five types are projected, dispatched on the FULL CloudEvents `type`:

| type | payload fields read | fact |
| --- | --- | --- |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `labelRef` (= subject), `trackingNumber`, `tier` | `package_labels` (one row per tracking number: its tier) |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageScanned` | `trackingNumber` (= subject), `scanId`, `scanType`, `at` | `journey_scans` (one row per applied scan) |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered` | `trackingNumber` (= subject), `deliveredAt` | `package_outcomes` (`DELIVERED`) |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageReturned` | `trackingNumber` (= subject), `returnedAt` | `package_outcomes` (`RETURNED`) |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost` | `trackingNumber` (= subject), `declaredAt` | `package_outcomes` (`LOST`) |

The other three published types (`com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided`,
`com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedOver`,
`com.retailnetwork.logistics.shipping-and-tracking.journey.PackageExceptionRaised`) are acknowledged and not projected:
no report needs them, and the handover is already a `PICKUP_DEPARTURE` scan.

Tables of the analytical database (`analytics/migrations/0001_delivery_facts`):

- `analytics_processed_events (event_id PK, event_type, occurred_at, applied_at)`: the dedupe set; `occurred_at` is the
  CloudEvents `time`, the basis of the freshness lag.
- `package_labels (tracking_number PK, label_ref, tier, labelled_at, event_id)`.
- `journey_scans (event_id PK, tracking_number, scan_id, scan_type, scanned_at)`.
- `package_outcomes (tracking_number PK, outcome IN (DELIVERED, RETURNED, LOST), outcome_at, event_id)`: a journey has
  exactly one terminal state.

Label events are keyed by `labelRef` and journey events by `trackingNumber`, so they sit on different partitions and may
arrive in any order. The model is therefore order-independent: it stores facts and the reports join them at query time.
A delivered package whose `LabelIssued` was not (yet) projected is reported under the tier `UNKNOWN`.

### 2. KPI definitions

Days are UTC calendar days of the **business instant in the payload** (`deliveredAt`, `returnedAt`, `declaredAt`), not
of the CloudEvents `time`.

**Touch.** A touch is an APPLIED journey scan (a `PackageScanned` event; a scan stored out of order or without an edge
raises none, ADR 0002 A5) whose `scanType` is a physical handling of the package. From the as-built scan vocabulary
(`internal/domain/journey/types.go`) the touch set is:
`PICKUP_DEPARTURE`, `DEPARTURE`, `TRANSIT_ARRIVAL`, `STATION_ARRIVAL`, `OUT_FOR_DELIVERY`, `DELIVERY_FAILED`.
Not touches: `SHIPMENT_CONFIRMED` (the vendor's confirmation, the package has not moved) and `DELIVERED` (the outcome
itself, not a handling before it). The journey is closed after `DELIVERED`, so every touch of a delivered package was
recorded before its delivery.

`GET /reports/package-touches?from=&to=`, per day of `deliveredAt` and per tier:

- `packagesDelivered` = number of packages with a `DELIVERED` outcome that day and tier;
- `meanTouchesPerPackage` = (sum of touches of those packages) / `packagesDelivered`;
- `p50TouchesPerPackage`, `p90TouchesPerPackage` = the continuous percentiles (`percentile_cont`, linear interpolation)
  0.5 and 0.9 of the touches per package.

**Attempt.** A delivery attempt is a `DELIVERY_FAILED` scan or the `DELIVERED` scan, so a delivered package's attempts
= its `DELIVERY_FAILED` scans + 1.

`GET /reports/delivery-outcomes?from=&to=`, per day of the outcome:

- `packagesDelivered`, `packagesReturnedToSender`, `packagesLost` = outcomes of that day; `resolved` = their sum;
- `firstAttemptDelivered` = delivered packages with no `DELIVERY_FAILED` scan;
- `firstAttemptSuccessRate` = `firstAttemptDelivered` / (`packagesDelivered` + `packagesReturnedToSender` +
  `packagesLost`);
- `meanAttemptsPerDeliveredPackage` = (`packagesDelivered` + `DELIVERY_FAILED` scans of the delivered packages) /
  `packagesDelivered`, `null` on a day without deliveries;
- `lostRate` = `packagesLost` / `resolved`; `returnToSenderRate` = `packagesReturnedToSender` / `resolved`.

`GET /reports/freshness`: `asOf` = the newest CloudEvents `time` the projection applied, `lagSeconds` = now - `asOf`
(never negative); both `null` until the first event.

Range rules: `from` and `to` are UTC dates (`YYYY-MM-DD`), both inclusive; both omitted = the 30 days ending today;
only `to` = the 30 days ending on `to`; only `from` = `from` .. today; `from` after `to` or a range over 366 days is a
400 problem. Empty results are empty arrays. Wire fields are camelCase (this repo's REST guideline, ADR 0010); the
routes are collection-shaped reports, `x-pagination-exempt: report`.

SQL only counts, sums and takes `percentile_cont`. The touch and attempt scan sets, the percentile ranks, the means, the
rates, the range rules and the freshness lag live in `internal/analytics/report`, a pure package that imports no other
internal package (arch test), is in the coverage scope and in `make mutation`. ONE contract suite runs against the
in-memory twin and against real Postgres.

### 3. Delivery policy (the projector)

- Consumer group `ANALYTICS_CONSUMER_GROUP` (default `shipping-and-tracking-analytics`), fixed and stable; at-least-once,
  the offset is committed only after the message was handled. A brand-new group starts at the earliest offset, so the
  model rebuilds from retained history.
- **Dedupe and projection in ONE analytical-database transaction**: `INSERT ... ON CONFLICT DO NOTHING` into
  `analytics_processed_events`; the fact is written (appended or upserted) only if that insert claimed the id. A failure
  leaves neither; a replay of an applied id is a no-op.
- Not a CloudEvents 1.0 message, or a CloudEvent of a type that is neither projected nor one of the three acknowledged
  types: skipped and committed past with a **sampled WARN** (the first, then at most one per minute with the count
  suppressed).
- **Dead-letter ONLY deterministic poison**: an undecodable payload, a subject that is not the aggregate id, a missing
  required field or timestamp, or a store rejection in Postgres class 22 (data exception) or 23 (integrity violation).
  The raw message goes to `retailnetwork.shipping-and-tracking.analytics.dlq` with `x-dlq-*` headers, then the offset is
  committed. A failed DLQ write is retried, so poison is never lost.
- **Transient failures** (database down, timeout, any other error) are retried forever with capped exponential backoff
  (200ms doubling to 5s) and never dead-lettered: a database outage must not turn into silently missing analytics. The
  cost is a blocked partition during the outage, visible in the logs and in `/reports/freshness`.

### 4. The binaries

- `cmd/st-projector`: the ONLY writer of the analytical database. Env: `ANALYTICS_DATABASE_URL` (required, a direct DSN:
  it runs the embedded migrations), `KAFKA_BROKERS` (required), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (default
  `:8091`, `/healthz` and `/readyz`), `LOG_LEVEL`, `OTEL_SERVICE_NAME` (default `shipping-and-tracking-projector`),
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`. It never opens the OLTP database. The migrations are
  embedded (`analytics/migrations.go`, `//go:embed migrations/*.sql`), so there is no `ANALYTICS_MIGRATIONS_PATH`.
- `cmd/st-reports`: read-only HTTP on `HTTP_ADDR` (default `:8092`): `GET /reports/package-touches`,
  `GET /reports/delivery-outcomes`, `GET /reports/freshness`, `/healthz`, `/readyz` (analytical-database ping). It reads
  `ANALYTICS_READER_DATABASE_URL`, falling back to `ANALYTICS_DATABASE_URL` for single-role local runs, through a pool
  whose sessions default to read-only transactions with a 15s statement timeout. No Kafka, no OLTP database, no call to
  any sibling, no auth (fleet rule). Errors are problem+json. The routes live in `apis/openapi.yaml` under the tag
  `reports` with a per-path server on `:8092`.
- Both ship in the one image (`/app/st-projector`, `/app/st-reports`).

### 5. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topic. Keeping it out of the OLTP database
means a heavy report cannot take OLTP connections, the two schemas evolve independently, the reports role can be
read-only on a database that holds nothing transactional, and the projector holds no OLTP credentials. Both DSNs are
direct (PgBouncer is not involved: the projector runs migrations).

### 6. Packaging

The chart gains an opt-in `analytics:` block (default `enabled: false`; the default render is byte-identical):
components `analytics-projector` (Deployment, no Service) and `analytics-reports` (Deployment + ClusterIP Service, an
opt-in Kong HTTPRoute `/api/shipping-and-tracking/reports` rewritten to `/reports`), an `analytics` Secret or
`analytics.database.existingSecret`. Render-time guards refuse `analytics.enabled` without a DSN source, without
`kafka.brokers` or without a consumer group. The projector pod never gets the OLTP `DATABASE_URL`; the reports pod gets
neither Kafka nor any DSN but the reader's.

## Consequences

- The `.events` topic gains one service-owned consumer group (`shipping-and-tracking-analytics`); its contract is
  unchanged. The reports are only as fresh as the projector, which `/reports/freshness` makes observable.
- The model starts at the earliest retained offset; packages delivered before that have no history.
- Touches count applied scans only: a scan the journey stored out of order is not a touch, and a handling that never
  produced a scan cannot be counted.
- A package whose label event was never retained reports under the tier `UNKNOWN`.
- A deploy needs a second database, two roles (projector read-write, reports read-only), the DLQ topic and the consumer
  group above (`warehouse-infra`).

## Alternatives considered and rejected

- **Project into the OLTP database**: no isolation of load, schema or credentials.
- **Read DeliveryAttempted from last-mile-delivery**: a sibling's topic; the journey's own `DELIVERY_FAILED` and
  `DELIVERED` scans carry the same attempts, so the context reports on its own facts only.
- **Fan the facts out to the analytics topic (`FanoutEncoder`)**: every fact the KPIs need is already on `.events`;
  doubling them would need a `UNIQUE (event_id, topic)` outbox change for no new information.
- **Dead-letter after N attempts for every failure**: drops analytics for events that failed only because the database
  was briefly down.
- **Day by CloudEvents `time`**: that is the moment the use case ran, not when the package was delivered; the payload
  instants are the business facts.
