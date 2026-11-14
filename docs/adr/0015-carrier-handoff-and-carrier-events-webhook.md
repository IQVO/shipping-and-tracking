# ADR 0015: carrier handoff and the external carrier webhook (C4)

## Status

Accepted (2026-11-13).

## Context

A service area can be declared `CARRIER_HANDOFF` by node-topology (RN-0010, ADR 0016 there):
the network no longer delivers the last mile itself, an external carrier collects the package
at a handoff node and reports progress back. This context owns the journey, so it must (a) know
each area's delivery mode, (b) choose WHICH of the area's declared carrier options takes each
package, deterministically, and (c) accept progress reports from carriers over REST.

The carrier choice cannot be a free operator decision at handoff: delivery-promise quotes
against a specific option's transit and the deterministic rule must be recomputable on both
sides. The webhook cannot carry a token: the fleet reverted the static bearer layer on
2026-09-11 and `TestNoAuthMiddlewareReintroduced` enforces that REST stays unauthenticated,
exactly like the existing `POST /journeys/{trackingNumber}/scans`, which already lets any
in-cluster caller append any scan.

## Decision

### Local area read model (new consumed type)

A new consumer group (env `ST_AREAS_CONSUMER_GROUP`, never a literal) reads
`retailnetwork.node-topology.events` and decodes
`com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` tolerantly into
`AreaModeView{postalPrefix, deliveryMode, carriers, assignmentVersion}`: absent `deliveryMode`
and `carriers` mean `NETWORK_ROUTE` with no options (the pre-C4 meaning). Rows are upserted
under the same assignment-version guard delivery-promise uses: an event older than the stored
version is acknowledged and dropped.

### WITH_CARRIER state and the CARRIER_HANDOFF scan

The journey gains the state `WITH_CARRIER` and the scan type `CARRIER_HANDOFF`. Edges:

- `CARRIER_HANDOFF`: `AT_STATION -> WITH_CARRIER`;
- `OUT_FOR_DELIVERY`: `WITH_CARRIER -> OUT_FOR_DELIVERY`;
- `DELIVERY_FAILED`: `OUT_FOR_DELIVERY -> WITH_CARRIER` when the journey carries a carrier
  choice (the carrier re-attempts from its own custody), `OUT_FOR_DELIVERY -> AT_STATION`
  otherwise (the ship-to and pickup behaviour is unchanged: the package rides back with the
  route).

`WITH_CARRIER` is not terminal and is silence-watched with the tier window (`SilenceWindow`).

### Deterministic carrier choice at handoff

When a `STATION_ARRIVAL` scan applies to a journey whose label prefix's area mode is
`CARRIER_HANDOFF`, the scans use case computes the chosen option over the CURRENT area view:

> sort the area's carrier options by (`costPerPackageCents` ascending, `transitMinutes`
> descending, `carrierId` ascending) and take the first.

The same rule is mirrored verbatim in delivery-promise (ADR 0026 there); both sides compute it
from the same area assignment version. The result is stored on the journey as
`chosenCarrierId`, `handoffNodeId`, `areaVersion` (migration `0005_journey_carrier`: three
nullable columns, all null or all set). A `CARRIER_HANDOFF` scan is accepted only when the
label prefix's area mode is `CARRIER_HANDOFF`; on a `NETWORK_ROUTE` area it is 422
`validation-failed`.

### PackageHandedToCarrier (new published type)

`apply` raises `PackageScanned` plus, on `AT_STATION -> WITH_CARRIER`,
`PackageHandedToCarrier{trackingNumber, assignmentId, orderId, carrierId, handoffNodeId,
destinationPostalPrefix, handedAt}` — full type
`com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedToCarrier`, dataschema
`urn:retailnetwork:shipping-and-tracking:events:PackageHandedToCarrier:v1`, subject/key = the
tracking number. No optional data fields; the event is raised once per journey (a repeat
handoff scan for the same journey is a duplicate scan). `destinationPostalPrefix` is the
label's prefix, read from the label aggregate in the same unit of work.

### POST /carrier-events (the webhook)

`reportCarrierEvent`, idempotency route `RouteCarrierEvent`, body
`{carrierId, carrierEventId, trackingNumber, status, at, proofCode?, locationCode?}` with
`status` in {`PICKED_UP`, `IN_TRANSIT`, `OUT_FOR_DELIVERY`, `DELIVERED`, `DELIVERY_FAILED`}:
`IN_TRANSIT` is accepted and counted, no scan. Every other status becomes a `RecordScan` fact
with scanId `<carrierId>:<carrierEventId>` (dedupe for free through `ErrDuplicateScan`) and
nodeId = the carrierId (for `PICKED_UP`: the handoff node stored on the journey):

- `PICKED_UP` -> a `CARRIER_HANDOFF` scan at the handoff node (state `AT_STATION` required; if
  the journey is already `WITH_CARRIER` it is a duplicate/no-op and nothing is stored);
- `OUT_FOR_DELIVERY` -> an `OUT_FOR_DELIVERY` scan;
- `DELIVERED` -> a `DELIVERED` scan (terminal; `PackageDelivered` flows to the promise
  judgement and the carrier-transit pairing);
- `DELIVERY_FAILED` -> a `DELIVERY_FAILED` scan (back to `WITH_CARRIER`).

Errors: 404 unknown tracking number; 409 duplicate-scan / illegal-transition
(stored-not-applied semantics preserved); 422 validation-failed (bad status, blank ids, `at`
in the future beyond a clock-skew allowance of five minutes).

**Authentication: none.** By the standing fleet decision this route is exactly as privileged
as the existing unauthenticated `POST /journeys/{trackingNumber}/scans` (cluster-internal
Kong route). The per-carrier token scheme is a recorded follow-up for the owner's auth-model
decision: adopting it needs a new ADR superseding the revert decision plus a deliberate
amendment of `TestNoAuthMiddlewareReintroduced` in the same commit.

## Consequences

- One new consumed type (`ServiceAreaAssigned`) in ADR 0003 and one new published type
  (`PackageHandedToCarrier`) in the ADR 0004 catalogue, both with AsyncAPI operations;
  the analytics projector acknowledges `PackageHandedToCarrier` without projecting it.
- `chosenCarrierId`/`handoffNodeId`/`areaVersion` persist on the journey (migration 0005) and
  are asserted in the repocontract round-trip.
- The deterministic choice rule is contract-pinned here and in delivery-promise's ADR 0026;
  changing it is a breaking change on both sides.
- A carrier silent past the tier window is caught by the silence policy (`WITH_CARRIER` is
  watched), not by a new sweeper.
- Kong route `/api/shipping-and-tracking/carrier-events` and the new consumer-group env
  (`ST_AREAS_CONSUMER_GROUP`) are deploy notes for warehouse-infra.
