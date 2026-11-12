# ADR 0014: relay the delivery-point flag by orderId

## Status

Accepted (2026-11-11). Extends 0003 (one consumed type) and 0004 (one published type).

## Context

Last-mile-delivery keeps a delivery-point profile per opaque stop key and flags
the point after two counted failed attempts (its ADR 0014), publishing
`DeliveryPointFlagged` with every tracking number of the stop. The shopper who
can fix the access problem knows the order, not the stop: the flag must reach
the order context. This context is the one that knows which journey (and so
which `orderId`) a tracking number belongs to; last-mile-delivery does not.

The day-of ETA window (ADR 0013) already crosses this seam exactly this way:
consume one stop event, apply per tracking number, publish per package.

## Decision

1. **Consume** `com.retailnetwork.logistics.last-mile-delivery.deliverypointprofile.DeliveryPointFlagged`
   (`{stopKey, reason, failures, trackingNumbers[], flaggedAt}`). `stopKey` and
   `failures` are read and dropped: this context keeps no address-derived value
   of a stop. A payload with no tracking numbers, a blank one, or a `reason`
   outside `NOT_HOME`/`ACCESS_PROBLEM` is a bad payload (dead-lettered).
2. **Store** `pointFlagReason` and `pointFlaggedAt` on each journey of the stop
   (`FlagDeliveryPoint`). Like the ETA window it is not a scan: the state does
   not change. A terminal journey is refused (`ErrJourneyClosed`), a `flaggedAt`
   not strictly newer than the stored one is refused (`ErrPointFlagStale`); both
   are counted and acknowledged. The two columns are null together and the
   reason is one of the two codes (CHECK, migration `0004_journey_point_flag`).
3. **Publish** `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDeliveryPointFlagged`
   per journey, one event with the journey's own `orderId`, so customer-ordering
   (ADR 0015 there) can show the advisory on the tracking view.
4. The relay is idempotent on the CloudEvents `id` (inbox) and runs in one unit
   of work with the per-journey saves and publishes.

## Consequences

- The advisory reaches the shopper per order without any address crossing a
  context boundary; the stop key never leaves its owner.
- There is no "cleared" event: the advisory ends when the package ends
  (delivered, returned, lost, voided), which the consumer decides.
- A flag that arrives for an unknown or terminal journey is counted and
  acknowledged: nothing can be relayed for it.
- The relayer adds no latency guarantee: the advisory appears when the flag is
  consumed, on the normal consumer path.
