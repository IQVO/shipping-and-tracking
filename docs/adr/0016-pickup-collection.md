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
