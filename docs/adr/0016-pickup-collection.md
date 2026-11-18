---
id: 0016
title: Pickup collection - deposit, collect and the uncollected hold
---

# Pickup collection - deposit, collect and the uncollected hold

## Status

Accepted (2026-11-13). The shipping-and-tracking half of RN-0011; the point
itself is node-topology's ADR 0017 and the quoted destination is
delivery-promise's ADR 0027.

## Context

A checkout may name a pickup point instead of a ship-to address. The label
must key the point (one stop of last-mile capacity serves every package of the
point), the wave's successful attempt must become a collection-ready package
rather than a doorstep delivery, the shopper (or the point's operator) reports
the collection, and a package nobody collects must re-enter the network as
reverse freight instead of occupying the point forever.

## Decision

- `IssueLabel` branches on customer-ordering's destination: a `PICKUP_POINT`
  order answers `{destinationKind, pickupPointId}` with no address fields. No
  address is read, checked or sealed; the stop key is the HMAC of
  `pickup-point|<pointId>`, so every package of one point shares one stop; the
  label's prefix is the point's own prefix. An unknown, retired or non-ACTIVE
  point is refused (`pickup-point-unavailable`); the local PickupPointView is
  node-topology's node stream folded locally (the same consumer group as the
  area modes, one group, two views).
- The journey gains `READY_FOR_COLLECTION` (the wave's DELIVERED attempt at a
  point is a `POINT_DEPOSITED` scan, never a doorstep DELIVERED) and
  `COLLECTED` (terminal, entered by a `COLLECTED` scan carrying an optional
  proof code). `READY_FOR_COLLECTION` is NOT terminal - the return path needs
  it. A FAILED attempt at a point keeps `DELIVERY_FAILED`: the package rides
  back to the station with the route.
- `PackageReadyForCollection` (with `holdUntil`) and `PackageCollected` are
  published; delivery-promise judges a pickup promise on the first, as a
  ship-to promise is judged on `PackageDelivered`.
- The uncollected sweep walks the expired holds and returns each package
  (`ReturnUncollected`): `PackageReturned` gains the reason `UNCOLLECTED`. The
  hold is `PICKUP_HOLD_DAYS` (default 7, a chart value, no secret).
- The consumed `NodeRegistered` copy carries the node's own `postalCode`: a
  pickup point's location is infrastructure data the producer already
  publishes on its contract - it is not a customer ship-to, and the ship-to
  fitness rule exempts exactly this consumed copy.

## Consequences

- No compartment inventory: a locker is a pickup point, holds are per package,
  and per-compartment rules are a named follow-up.
- The point's operator reports collection through the existing scans route (a
  `COLLECTED` scan); a dedicated partner-facing surface is a follow-up.
- The collection proof (`proofCode`) is optional and carried in the open: it
  names nobody.

## Addendum 2026-11-18: a status fact changes only the state of a pickup point

The Decision above is unchanged. This addendum records how the
PickupPointView is written, which the Decision did not spell out, after two
defects were found in the first implementation.

### What was wrong

- `NodeActivated`, `NodeSuspended` and `NodeRetired` carry a `nodeId` and an
  instant or a reason, nothing else. The fold wrote them as a whole row
  `{id, state}` through a full-row upsert, so the first status fact of a
  pickup point erased its serving station and its prefix, and a status fact of
  a node of ANY other type created a pickup-point row.
- Every pickup point is registered `PROPOSED` and becomes `ACTIVE` by exactly
  such a fact, so no pickup label could be issued from event-fed state: a
  `PROPOSED` point was refused, and an `ACTIVE` one had an empty prefix, which
  `label.Issue` refuses as 422 `validation-failed` ("destinationPostalPrefix
  must be 1 to 16 characters from A-Z 0-9").
- `NodeRegistered` applied a second time (a rebuild) wrote the whole row as
  well and put the point back to `PROPOSED`.
- `shipto.ErrPickupPointUnavailable` had no entry in the HTTP problem
  catalogue: the refusal this ADR names `pickup-point-unavailable` answered
  500 `internal-error`.

### The rule

- The PickupPointView has two writes and neither touches the other's columns.
  A **registration** (`Register`) writes the descriptive data of a pickup
  point, its serving station and its prefix; a point the view does not hold is
  inserted `PROPOSED`, a point it holds keeps its state, whatever it is. A
  **status fact** (`SetState`) writes the state of a point the view holds and
  nothing else.
- A status fact of an id the view does not hold is claimed in the inbox and
  acknowledged without an effect; no row is made. Node facts are keyed by the
  node id, so a node's registration precedes its status facts on the
  partition: no row means "not a pickup point", never "not yet". It is the
  rule of an unknown tracking number (ADR 0002 A17, ADR 0003), with one
  difference: it is neither logged nor counted, because it is the normal case
  of every node that is not a pickup point. It is never dead-lettered: the
  fact is well-formed.
- `NodeRegistered` applied again restores station and prefix and never the
  state: an `ACTIVE`, `SUSPENDED` or `RETIRED` point stays what it is.
- **A retired point keeps its row, in state `RETIRED`** (it was deleted
  before). `RETIRED` is terminal in node-topology, and it is terminal here: no
  later status fact changes it. A deleted row forgets that the point was
  retired, so a replayed registration would bring it back as `PROPOSED` and
  its replayed `NodeActivated` would make it labelable again until the
  replayed `NodeRetired` arrived. For a label nothing changes: an unknown
  point and a `RETIRED` one are both refused `pickup-point-unavailable`.
- `pickup-point-unavailable` is 422, as this ADR and the error's own comment
  always said, and `POST /labels` declares it. A test now keeps the problem
  catalogue, the typed errors of the model and `apis/openapi.yaml` in step
  (`internal/adapters/inbound/http/problem_parity_test.go`,
  `problem_typed_errors_test.go`).

### The repair

Rows written before this fix stay damaged until the registrations are applied
again: the procedure, the inbox identity it has to clear and its dependency on
the topic's retention are in the runbook, "Repairing the pickup point view".
No migration is needed: the table and its columns are unchanged.

### Not decided here

- Whether `pickup-point-unavailable` is stored under the caller's
  `Idempotency-Key`. It follows the point's state, which changes, so under the
  rule of ADR 0018 it is a candidate for the refusals that are not stored;
  today it is stored, as every 4xx is that is not on that list, and replayed
  for the 24 hours the key lives. While it answered 500 it was not stored. That
  belongs to the fleet-wide change of that rule.
- The prefix of a pickup point is its registered `postalCode` as published. A
  code with a character outside A-Z 0-9 (node-topology's own example is
  `07750-000`) fails the label's prefix rule and the label is refused 422
  `validation-failed`. ADR 0018 normalises a node's postal code for a return
  label; this ADR does not for a pickup label.
