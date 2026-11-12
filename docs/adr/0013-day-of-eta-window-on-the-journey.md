# ADR 0013: day-of ETA window on the journey

## Status

Accepted (2026-11-11).

## Context

last-mile-delivery plans a route from the stops its caller sends and starts it with `StartRoute`. It holds no
geography, travel time or service time, so the ETA it can state is a deterministic spread of the route's stops over the
wave's delivery window. At the start of a route it raises one `StopEtaWindowAssigned` per stop, after every
`PackageOutForDelivery`, on the same topic and under the same Kafka key (the `routeId`), so the two stay ordered.

The "journey timeline" of this context is `PackageJourney`: eight scan types and nine states. An ETA is not a scan, and a
new scan type would enter the state-machine edge table (ADR 0002 A5, A13). The customer-facing context
(customer-ordering) reads journey facts from this context's topic only; it does not consume last-mile-delivery.

## Decision

### Consumed: `StopEtaWindowAssigned`

- Type `com.retailnetwork.logistics.last-mile-delivery.route.StopEtaWindowAssigned`, topic
  `retailnetwork.last-mile-delivery.events` (consumer group `LASTMILE_CONSUMER_GROUP`, already required). Dispatch is on
  the full type string in `LastMileHandlers`.
- Data relied on: `routeId`, `trackingNumbers[]` (at least 1, stop order),
  `etaStart`, `etaEnd` (RFC 3339 UTC). The CloudEvents `time` is the route start instant and is the `assignedAt`.
  Decoding is tolerant: unknown fields are ignored; blank `trackingNumbers` or `etaStart >= etaEnd`
  is a bad payload and is dead-lettered.
- Use case `ApplyEtaWindows`, same shape as `ApplyScans`: one unit, `Inbox.Claim(consumer, eventId)` first, then per tracking
  number: unknown journey counted, closed journey counted, stale ETA counted, otherwise saved and published.
- The use case reads and stores no address; `stopKey` is received and dropped (ST keeps none).

### The aggregate

- `PackageJourney` gains `etaStart`, `etaEnd`, `etaAssignedAt` (struct, `Snapshot`, `Restore`, `Snapshot()` builder).
  Invariant checked by `Restore`: all three zero, or `etaStart < etaEnd` and `etaAssignedAt` non-zero.
- Command `AssignEta(start, end, assignedAt, now)`: a terminal journey is refused (`ErrJourneyClosed`); an `assignedAt`
  not strictly newer than the stored one is refused (`ErrEtaStale`, acknowledged by the consumer); otherwise it stores the
  window, increments `version` and raises `PackageEtaWindowAssigned`. The state does not change.
- `EtaCurrent()` is true when the state is `OUT_FOR_DELIVERY`, `etaAssignedAt` is non-zero and it is not before
  `lastScanAt`. This one rule defines "current" in this context and in customer-ordering: yesterday's window is stale as
  soon as a newer OUT_FOR_DELIVERY scan exists, a failed attempt (state back to AT_STATION) hides it, and no terminal state
  shows it.
- Migration `0003_journey_eta`: `package_journeys` gains `eta_start`, `eta_end`, `eta_assigned_at` (`timestamptz NULL`,
  CHECK: all null or all non-null and `eta_start < eta_end`).

### Published: `PackageEtaWindowAssigned`

- Type `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageEtaWindowAssigned`; source
  `/retailnetwork/shipping-and-tracking`; dataschema `urn:retailnetwork:shipping-and-tracking:events:PackageEtaWindowAssigned:v1`;
  topic `retailnetwork.shipping-and-tracking.events`; subject and Kafka key = the trackingNumber.
- Data (all required): `trackingNumber`, `assignmentId`, `orderId`, `etaStart`, `etaEnd`, `assignedAt` (RFC 3339 UTC).
- Ordering: last-mile-delivery writes `PackageOutForDelivery` before the ETA rows under one key and this context handles
  them in that order, so the OUT_FOR_DELIVERY `PackageScanned` precedes `PackageEtaWindowAssigned` under one key. The design
  still tolerates any order: `EtaCurrent` hides a window until the scan and the rule agree.

### Journey view

REST `getJourney`, MCP `get_journey` and A2A `get_journey` gain optional `etaStart` and `etaEnd`, present only while
`EtaCurrent()` holds. The window is shown as received: it is never clipped to the promised delivery window.

## Consequences

- No new env var, topic, consumer group, chart value or secret. Migration 0003 is additive and runs at boot.
- A consumer ignores a type it has no handler for and commits past it, so this context must be deployed before
  last-mile-delivery starts raising the event (consumers first).
- The ETA is not a scan: the scan state machine, the silence policy and the edge table are untouched.
- The analytics projector reads this context's own topic and acknowledges the new type without projecting a fact.
- The E3 hit-rate KPI lives in last-mile-delivery's report; nothing is measured here.
- Rollback: the columns stay; silence from last-mile-delivery simply stops new windows.
