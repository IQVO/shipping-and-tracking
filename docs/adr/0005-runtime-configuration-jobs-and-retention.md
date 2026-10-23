# 0005. Runtime: configuration, periodic jobs, retention of the technical tables

## Status

Accepted (2026-10-23)

## Context

ADR 0002 fixes the aggregates and ADR 0003 the consumed contracts. This ADR fixes what the single `cmd/api`
process does at runtime and how it is configured. It follows the fleet conventions: real Postgres and real
Kafka only, an always-on outbox relay, dependency-aware `/readyz`, mandatory consumer groups, and a boot that
refuses a misconfigured environment before it opens anything.

## Decision

### One process, five concerns

`cmd/api` runs, in this order of shutdown: the HTTP server, the four Kafka consumers (vendor, linehaul,
last-mile, catalog), three periodic jobs, and the outbox relay. The relay stops last, so nothing committed by an
earlier stage is stranded. The Postgres pool closes after all of them. If a consumer, a job loop or the relay
returns on its own, the process exits non-zero and the orchestrator restarts it.

### Configuration (all validated at boot, each error names its variable)

| Variable | Rule |
| --- | --- |
| `DATABASE_URL` | required; there is no in-memory mode |
| `KAFKA_BROKERS` | required |
| `CUSTOMER_ORDERING_URL` | required, absolute `http(s)` URL; `CUSTOMER_ORDERING_TIMEOUT` optional (default 3 s) |
| `SHIP_TO_ENCRYPTION_KEY` | required, base64 of exactly 32 bytes (AES-256-GCM) |
| `STOP_KEY_SECRET` | required, at least 16 bytes (HMAC-SHA-256 of the normalized ship-to) |
| `VENDOR_`, `LINEHAUL_`, `LASTMILE_`, `CATALOG_CONSUMER_GROUP` | all required |
| `EVENT_PUBLISHER` | unset or `kafka`; `log` is a boot error |
| `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS` | defaults 1 s and 20 (0 = unlimited) |
| `SILENCE_SWEEP_INTERVAL`, `HOUSEKEEPING_INTERVAL` | defaults 5 min and 1 h |

A secret value is never part of an error message or a log line. The sealer writes the first 8 hex characters of
the SHA-256 of its key into each snapshot as `key_id`, so a later rotation can tell which key sealed which row.
Rotation itself is not built (a model gap, listed in the final report).

### Periodic jobs

| Job | Does | Period |
| --- | --- | --- |
| `silence-sweep` | `SweepSilence`: raises `NO_SCAN` exceptions and declares packages lost (ADR 0002 A7, A8) | `SILENCE_SWEEP_INTERVAL` |
| `ship-to-purge` | `PurgeShipTo`: deletes snapshots 30 days after their journey closed (A9) | `HOUSEKEEPING_INTERVAL` |
| `housekeeping` | deletes expired technical rows (below) | `HOUSEKEEPING_INTERVAL` |

Every job runs once at start and then on its period. A failing run is logged and retried on the next tick; the
loop never stops for it. With more than one replica the jobs may run concurrently; this is safe because each
journey is reloaded and decided in its own unit under the optimistic `version` (a lost race is a counted
conflict, seen again on the next tick), and the purges are idempotent deletes.

### Retention of the technical tables

| Table | Rule |
| --- | --- |
| `idempotency_keys` | 24 hours after creation (the replay window of `Idempotency-Key`) |
| `outbox_events` | 7 days after `published_at`; an unpublished row is never deleted |
| `processed_events` | 30 days after `processed_at` (far beyond Kafka's retention of the consumed topics) |

### Idempotency-Key

The key is scoped per route (`idempotency_keys` primary key `(route, key)`). All three POST routes require it.
The middleware stores the response in the same transaction as the work, so a replay returns the first answer
byte for byte. Responses below 500 are cached, with one exception: a `concurrent-update` 409 is not (it is
retryable, so replaying it would pin a transient conflict). A key reused with another body, a missing key, a key
longer than 128 characters and a malformed body are all 422 `validation-failed` (the contract has no 400; ADR
0002 A24).

### Not built

No telemetry exporter and no `/metrics` endpoint: the template carries neither dependency and the model asks for
neither. Health and readiness are the only operational endpoints.

## Consequences

- A deployment sets six required variables more than a plain service (two secrets, the customer-ordering URL and
  four groups); the boot error tells which one is missing.
- The sweep period bounds how late an exception can be: a silence window plus at most one period.
- The `processed_events` retention is a decision, not a model fact; it can be shortened if the table grows.
