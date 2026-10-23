# ADR 0003: consumed contracts, local read models, relationships and the one synchronous call

## Status

Accepted (2026-10-23).

## Context

shipping-and-tracking folds facts from four producers into one timeline and prints labels from an
address it must fetch. The model lists six consumed events, one synchronous call (`GetShipTo`) and the
relationships below. contracts-delta E3, E10, E15 and E16 add shapes. Producers' AsyncAPI files are read
from their repos when pushed (`git show origin/feature/service:apis/asyncapi.yaml`); at the time of writing
none of the four had one, so the consumed shapes are the model's fields, the contracts-delta additions and
the wire conventions. Each consumer vendors a tolerant local copy of what it uses and ignores unknown fields.

## Decision

### Consumed events

All are read from `retailnetwork.<producer>.events`, in one consumer group per topic, dispatched on the full
CloudEvents `type`, deduplicated on the CloudEvents `id` in `processed_events` (the inbox).

| Producer (relationship) | Full type | Fields relied on | Effect |
| --- | --- | --- | --- |
| vendor-connectivity (Customer/Supplier) | `com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed` | `poNumber`, `nodeId`, `trackingNumbers[]`, `shippedAt` | per tracking number a `SHIPMENT_CONFIRMED` scan |
| linehaul-transportation (Conformist) | `com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted` | `originNodeId`, `trackingNumbers[]`, `departedAt`, `departureKind`, `nodeType` | `PICKUP_DEPARTURE` or `DEPARTURE` scan |
| linehaul-transportation | `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | `destinationNodeId`, `trackingNumbers[]`, `arrivedAt`, `nodeType` (fallback `destinationNodeType`) | `STATION_ARRIVAL` or `TRANSIT_ARRIVAL` scan |
| last-mile-delivery (Conformist) | `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | `trackingNumber`, `stationNodeId`, `at` | `OUT_FOR_DELIVERY` scan |
| last-mile-delivery | `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | `trackingNumber`, `outcome`, `at` | `DELIVERED` or `DELIVERY_FAILED` scan |
| last-mile-delivery | `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` (also accepted: `...last-mile-delivery.route.PackageReturnInitiated`) | `trackingNumber` | `ReturnToSender` |
| offer-availability (Conformist) | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `catalogItemId`, `title`, `lengthMm`, `widthMm`, `heightMm`, `weightG`, `hazmat`, `fragile` | upsert `catalog_dimensions` |

The entity segment of `PackageReturnInitiated` is last-mile-delivery's choice (the model names no
publisher aggregate for it); both likely strings are registered and the right one is confirmed when
last-mile-delivery pushes its AsyncAPI (a model gap in the final report). Types not in the table are
acknowledged and ignored.

### Tolerant decoding and failure handling

- Unknown fields are ignored; a missing optional field takes the default of ADR 0002 A20.
- A message that is not a CloudEvents 1.0 JSON event, has a malformed `data`, or whose required fields are
  blank is a poison message: it goes to the dead-letter topic `<topic>.dlq` with the failure reason in
  headers, then the offset is committed. A transient failure (database, `ErrConcurrentUpdate` after the
  retries, a `ReturnToSender` that arrives before the journey is back `AT_STATION`) is retried with a
  bounded backoff and dead-lettered after the attempt cap.
- `ErrDuplicateScan`, `ErrScanOutOfOrder`, `ErrIllegalTransition` on a scan and `ErrJourneyClosed` are
  acknowledged (logged); an unknown tracking number is counted and acknowledged (A17).

### Local read model

`CatalogDimensionsView` (`catalog_dimensions`) is the only read model. It accepts no command and enforces no
invariant (ADR 0002 A18).

### Synchronous call

`GetShipTo` (`GET {CUSTOMER_ORDERING_URL}/orders/{orderId}/ship-to`, response `name`, `line1`, `line2`,
`city`, `region`, `postalCode`, `countryCode`, `phone`) is the model's only `sync_call`. It is a port
(`ShipToGateway`) with an HTTP adapter under `internal/adapters/outbound/customerordering`. The call precedes
the transaction. The address lives in memory for the duration of the request only: it is normalized for the
`stopKey`, encrypted into the snapshot and dropped (ADR 0002 A9, A10, A11).

### Relationships

- vendor-connectivity calls `POST /labels` (Customer/Supplier, in-organization) and receives
  `{labelRef, trackingNumber, carrier}`.
- linehaul-transportation, customer-ordering and last-mile-delivery consume `LabelIssued` / `LabelVoided`
  (E10, E16); delivery-promise, order-sourcing and customer-ordering consume the `Package*` events; node-performance
  consumes `PackageHandedOver`. This context is an Open Host Service (`GET /journeys/{trackingNumber}`).

## Consequences

- Four mandatory consumer-group variables: `VENDOR_CONSUMER_GROUP`, `LINEHAUL_CONSUMER_GROUP`,
  `LASTMILE_CONSUMER_GROUP`, `CATALOG_CONSUMER_GROUP`. A missing one is a boot error naming it.
- No other sibling is contacted at runtime.
