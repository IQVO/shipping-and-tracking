---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause, check and fix for the real failure modes of Shipping & Tracking: boot errors, readiness, label issuing, scans, outbox, consumers and the periodic jobs."
---

# Troubleshooting

Each entry reads symptom, cause, check, fix. Log message texts are the ones the code writes; see [Observability](/docs/operations/observability) for the full list. The operational procedures behind the fixes are in the [Runbook](/docs/operations/runbook). Commands assume the local kind cluster namespace `warehouse-systems`.

## The process does not start

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Exit 1 at once, log `service exited with error` naming a variable | A mandatory variable is missing or invalid. `loadConfig` validates everything before anything is opened (`cmd/api/config.go`) | `kubectl -n warehouse-systems logs deploy/shipping-and-tracking --previous`. Messages: `DATABASE_URL is required`, `KAFKA_BROKERS is required`, `<X>_CONSUMER_GROUP is required`, `SHIP_TO_ENCRYPTION_KEY is required`, `STOP_KEY_SECRET is required`, `CUSTOMER_ORDERING_URL is required` | Set the variable in the Secret or ConfigMap and restart. Values are never echoed |
| `SHIP_TO_ENCRYPTION_KEY: bad key: want 32 bytes, got N` or `not base64` | The key is not base64 of exactly 32 bytes | decode the Secret value with `base64 -d` and count the bytes: it must be 32 | Generate a new key (`openssl rand -base64 32`). Existing snapshots sealed with the old key are no longer openable (nothing reads them back; they are purged 30 days after the journey closes) |
| `STOP_KEY_SECRET: ...` | The secret is shorter than 16 bytes | length of the Secret value | Use at least 16 bytes. Changing it changes the `stopKey` of labels issued afterwards; issued labels keep theirs |
| `EVENT_PUBLISHER=log is not supported` or `unknown value` | The deployment sets a publisher this service does not have | `kubectl get cm` for the env | Set `kafka` or remove the variable. The chart refuses the render as well |
| `CUSTOMER_ORDERING_URL "..." is not an absolute http(s) URL` | Missing scheme or host | the ConfigMap value | Use `http://customer-ordering.warehouse-systems.svc.cluster.local:80` style URLs |
| `CUSTOMER_ORDERING_TIMEOUT: "x" is not a positive duration` | Malformed timeout (the only duration that is a boot error) | the value | Use `3s` or `3` |
| `cmd/a2a` exits with `A2A_AGENT_URL is required` or `is not an absolute http(s) URL` | The Agent Card must advertise a dialable URL | the pod env | Set `a2a.agentUrl`, or leave it empty so the chart renders the in-cluster Service URL |
| Pod stays not started, log `retrying` with `op="run migrations"` or `"ping postgres"` | Postgres, or PgBouncer, is not answering yet; boot retries with backoff then gives up (`internal/bootretry`) | `kubectl logs` for the `err` field; check the database Secret | Fix connectivity. If `DATABASE_URL` goes through PgBouncer, give `MIGRATIONS_DATABASE_URL` a direct DSN, otherwise the migration advisory lock fails |
| The chart renders nothing or refuses | A required value is empty, `terminationGracePeriodSeconds` is not above 30 s plus the drain delay, or `extraEnv` sets a variable the chart owns | `helm template` error text; `charts/shipping-and-tracking/tests/render_test.sh` | Fix the values |

## The process is up but not ready

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `/readyz` 503, problem `dependency-unavailable` naming `kafka` | No broker answers a metadata request within 2 s | log `readiness: dependency is not ready` with `dependency=kafka`; `KAFKA_BROKERS` | Fix the broker or the address. `/healthz` stays 200 and REST writes still succeed, events queue in `outbox_events` |
| `/readyz` 503 naming `postgres` | Ping failed | same log with `dependency=postgres` | Fix Postgres or PgBouncer. Writes fail with 500 `internal-error` meanwhile |
| `/readyz` 503 `{"status":"not_ready"}` and the pod is terminating | Normal drain: `SHUTDOWN_DRAIN_DELAY` after SIGTERM | pod phase | None |
| Pod restarts with `consumer stopped`, `outbox relay stopped` or `... stopped unexpectedly: exiting so the orchestrator restarts the service` | A consumer, a job loop or the relay returned on its own; the process exits non-zero on purpose (`watchWorkers`) | the log line before the exit gives the cause (`error` field) | Fix the cause; the restart itself is the recovery |

## REST calls

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| 400 `idempotency-key-required` on a POST | The `Idempotency-Key` header is missing (all three POSTs need it) | request headers | Send a unique key per logical request |
| 422 `validation-failed` with detail `the Idempotency-Key is longer than 128 characters` | Key above `MaxIdempotencyKeyLen` | key length | Shorten it |
| 422 `validation-failed` with detail `the Idempotency-Key was used with a different request body` | Same key, different body (route plus key identify the request; a hash of the body is compared) | the two bodies | Use a new key for a new request. Keys expire after 24 h |
| 422 `validation-failed` from `POST /labels` with `tier ... is not one of ...`, `packageNo must be between 1 and 2147483647`, `... is required` | Body fails `internal/adapters/inbound/http/handlers.go` validation; unknown JSON fields are rejected as well | the detail text names the field | Fix the request |
| 422 `validation-failed` from `POST /labels` and the body is valid | customer-ordering answered 404 for the order, mapped to `ports.ErrNotFound` while issuing, the order is unknown to it | `GET {CUSTOMER_ORDERING_URL}/orders/{orderId}/ship-to` | Issue the label for an order that exists |
| 503 `ship-to-unavailable` | customer-ordering timed out, was unreachable, answered a status other than 200 or 404, or returned a body that is not JSON (`internal/adapters/outbound/customerordering/gateway.go`) | counter `shipping.labels.issued{outcome="rejected",reason="ship-to-unavailable"}`; call the URL above from the pod | Recover customer-ordering or raise `CUSTOMER_ORDERING_TIMEOUT`. No label is stored; retrying with the same key is safe |
| 422 `validation-failed` with detail `ship-to ... is required`, `countryCode must have 2 characters` or `... is longer than 200 characters` | customer-ordering answered 200 but the ship-to fails `Address.Validate` (`internal/application/shipto/shipto.go`); the detail names the field, never the value | the response of the ship-to URL | Fix the order's address upstream |
| 409 `label-already-issued` | A label already exists for `(poNumber, packageNo)` and this is a new idempotency key; checked before customer-ordering is called | `SELECT label_ref, status FROM shipping_labels WHERE po_number = $1 AND package_no = $2` | Treat as success if the label is the wanted one; otherwise void it first |
| 409 `tracking-number-taken` | The generated tracking number collided with an existing one twice in a row (the use case retries once with a fresh number) | log `request failed` | Retry with a new key |
| 409 `concurrent-update` | Two writers changed the same aggregate; optimistic `version` check lost | repeated occurrences mean a hot aggregate | Retry. The Kafka consumers also retry it |
| 404 `not-found` on `GET /journeys/{trackingNumber}` or `POST /labels/{ref}:void` | Unknown reference. Journey rows are created by label issuing | `SELECT state FROM package_journeys WHERE tracking_number = $1` | Check the tracking number or label reference |
| 409 `void-after-handover` | The package left the sender (state beyond `LABELLED`) | journey state | A handed-over package cannot be voided |
| 409 `duplicate-scan` | The `scanId` was already recorded for that journey | `SELECT * FROM journey_scans WHERE tracking_number = $1` | Nothing to fix, the scan is stored once |
| 409 `scan-out-of-order` | The scan is older than the last applied scan; it is stored with `applied=false` | the `at` of the scan versus `last_scan_at` | Expected for late data. It does not move the state |
| 409 `illegal-transition` | The scan type has no edge from the current state (see the state machine in [Use cases](/docs/ddd/use-cases)); stored with `applied=false` | `state` of the journey | Send the intermediate scans first, or ignore |
| 409 `journey-closed` | The journey is `DELIVERED`, `RETURNED`, `LOST` or `VOIDED` | journey state | Closed journeys accept no scans |
| 404 `not-found` on `POST /labels/{ref}:void` or a path that matches no route | The void route is `POST /labels/{labelRef}:void` with a literal colon | the URL | Do not encode the colon as a path separator |
| 422 `validation-failed` with a body that looks fine | A NUL character (U+0000) in the query or body is rejected as a client error by `rejectNUL` instead of reaching Postgres; a NUL in the path is a 404 | the request bytes | Remove the NUL |

## Events

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `outbox_events` rows with `published_at IS NULL` pile up | Kafka unreachable, or rows parked at the attempt cap | `SELECT count(*), max(attempts) FROM outbox_events WHERE published_at IS NULL` | Fix Kafka. For parked rows (`attempts >= OUTBOX_MAX_ATTEMPTS`, log `row reached the attempt cap and is excluded from future claims`) run `UPDATE outbox_events SET attempts = 0 WHERE id = <id>` after the cause is fixed. The same `event_id` is republished, consumers dedupe |
| Label issued but downstream (for example customer-ordering) never sees `LabelIssued` | The row is still in the outbox, or the relay is down | `SELECT event_type, published_at, last_error FROM outbox_events ORDER BY id DESC LIMIT 5` and the `outbox relay` log lines | As above |
| A consumed event has no effect and the log says `skipping an event that can never be applied` | The event is valid but cannot be applied, for example an unknown tracking number or an illegal transition | the `type` and `event_id` in the log | If the producer is wrong, fix it there. If the message must be re-driven, look in `<topic>.dlq` |
| Messages land in `<topic>.dlq` | Transient failures exhausted the retries (log `handling failed after the maximum attempts`), or `dead-lettering an event that can never be applied` | read the dead-letter message headers for the cause | Fix the cause, then replay by hand; there is no automatic replay |
| `skipping a message that is not a valid CloudEvent` | A producer wrote a non-CloudEvents message on a consumed topic | the `consumer` and `topic` fields | Fix the producer. The offset advances |
| Consumer retry logs only, on an empty topic | Topics are created by the first writer; if the upstream has not written yet there is nothing to read | `consumer running` lines with the topic | Upstream gap, not a fault here |
| A redelivered event changes nothing | Normal: `processed_events` holds `(consumer, event_id)` and the handler skips it (`skipped=true` in `event processed`) | the log | None. Rows are purged after 30 days |
| After re-pointing a consumer group id the topic is re-read from the start | A new group has no committed offsets | the env var | Keep the four group ids stable |

## Jobs and data

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| No `PackageExceptionRaised` for a journey that has gone quiet | The sweep only watches journeys past `LABELLED`; the window depends on tier and state (6 h same-day, 24 h next-day, 48 h two-day, 72 h standard, 12 h out-for-delivery), and the sweep runs every `SILENCE_SWEEP_INTERVAL` (5 m) | log `scheduled job finished` with `job=silence-sweep` and `result=raised=N lost=N conflicts=N` | Wait for the next tick. `conflicts` above zero means a concurrent update; the next pass retries |
| `scheduled job failed; trying again on the next tick` | The job's database call failed | the `error` field | Fix the database; the next tick retries |
| `ship_to_snapshots` rows older than 30 days after close | `ship-to-purge` has not run or failed | `SELECT count(*) FROM ship_to_snapshots s JOIN package_journeys j ON j.label_ref = s.label_ref WHERE j.closed_at < now() - interval '31 days'` | Check the `ship-to-purge` job lines; `HOUSEKEEPING_INTERVAL` is 1 h |
| `idempotency_keys`, `outbox_events` or `processed_events` growing | `housekeeping` deletes keys after 24 h, relayed outbox rows after 7 days and inbox rows after 30 days | log line `idempotency_keys=N outbox_events=N processed_events=N` | Unrelayed outbox rows are never deleted; see the outbox entry above |

## MCP and A2A

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| MCP client gets a tool error for an unknown tracking number | The tool returns the not-found problem as a tool error | the tool result | Use an existing tracking number |
| A2A Agent Card advertises the wrong URL | `A2A_AGENT_URL` points elsewhere than clients reach | `curl <host>/.well-known/agent-card.json` | Set `a2a.agentUrl` |
| MCP or A2A binary works on a fresh database before `cmd/api` ran | They run the embedded migrations themselves; the advisory lock makes concurrent starts safe | boot log | None |
