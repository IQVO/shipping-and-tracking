---
id: events
title: Event catalogue
sidebar_label: Events
description: "The CloudEvents Shipping & Tracking publishes on retailnetwork.shipping-and-tracking.events and the seven types it consumes from four topics."
---

# Event catalogue

Contract: `apis/asyncapi.yaml` (AsyncAPI 3.0.0), catalogued in [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) (published) and [ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models) (consumed). `TestEventCatalogueMatchesContract` and `TestConsumedTypesAreDocumented` keep the spec, the code and the ADRs aligned. Field-level detail is on [Domain events](/docs/ddd/domain-events).

## Envelope

Every message is CloudEvents 1.0 in **structured** content mode with Kafka header `content-type: application/cloudevents+json; charset=UTF-8` (`internal/adapters/kafka/cloudevents/cloudevents.go`):

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | UUID, minted once when the event is encoded and stored in `outbox_events.event_id`: a relay retry republishes the same id |
| `source` | `/retailnetwork/shipping-and-tracking` |
| `type` | `com.retailnetwork.logistics.shipping-and-tracking.<entity>.<EventName>` with entity `label` or `journey` |
| `subject` | the aggregate root id: `labelRef` for label events, `trackingNumber` for journey events |
| `time` | RFC 3339 UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:shipping-and-tracking:events:<EventName>:v1` |

The Kafka key is the subject, so one aggregate's events stay ordered on one partition. `data` is camelCase, optional fields are omitted when unset, arrays are always present. A breaking payload change is a new `.v2` type, never a mutation. Trace context (`traceparent`, `tracestate`) rides as additional Kafka headers.

## Published: `retailnetwork.shipping-and-tracking.events`

Delivery is at-least-once through the transactional outbox: the aggregate rows and the encoded events are written in one database transaction and the relay (`internal/adapters/outbound/outbox/relay.go`) drains them. Consumers dedupe on `id`.

| Event (type suffix) | Raised by | Payload (`data`) | Known consumers (per the AsyncAPI) |
| --- | --- | --- | --- |
| `label.LabelIssued` | `IssueLabel` | `labelRef`, `trackingNumber`, `poNumber`, `assignmentId`, `orderId`, `packageNo`, `tier`, `carrier`, `stopKey`, `destinationPostalPrefix` | linehaul-transportation, customer-ordering, last-mile-delivery |
| `label.LabelVoided` | `VoidLabel` | `labelRef`, `trackingNumber`, `reason` | customer-ordering, linehaul-transportation |
| `journey.PackageScanned` | every applied scan | `trackingNumber`, `scanId`, `nodeId`, `scanType`, `at`, `state` | none named |
| `journey.PackageHandedOver` | `LABELLED` to `HANDED_OVER` | `trackingNumber`, `poNumber`, `nodeId`, `handedOverAt` | node-performance |
| `journey.PackageDelivered` | `OUT_FOR_DELIVERY` to `DELIVERED` | `trackingNumber`, `assignmentId`, `orderId`, `deliveredAt` | delivery-promise, order-sourcing, customer-ordering |
| `journey.PackageExceptionRaised` | silence sweep, reason `NO_SCAN` | `trackingNumber`, `assignmentId`, `orderId`, `reason`, `at` | delivery-promise, customer-ordering |
| `journey.PackageReturned` | `AT_STATION` to `RETURNED` | `trackingNumber`, `orderId`, `returnedAt` | delivery-promise |
| `journey.PackageLost` | `IN_TRANSIT` to `LOST` | `trackingNumber`, `orderId`, `declaredAt` | delivery-promise |

A handover or delivery raises two events in one transaction: `PackageScanned` and the milestone event. A scan stored out of order or without an edge raises nothing. No event carries a ship-to address; `stopKey` is the HMAC-SHA-256 (64 lower-case hex characters) of the normalised address and `destinationPostalPrefix` its first five letters or digits.

Example (`LabelIssued`, from the AsyncAPI; the values are synthetic):

```json
{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c01",
  "source": "/retailnetwork/shipping-and-tracking",
  "type": "com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued",
  "subject": "lbl-7c1f",
  "time": "2026-10-23T12:00:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:shipping-and-tracking:events:LabelIssued:v1",
  "data": {
    "labelRef": "lbl-7c1f",
    "trackingNumber": "STT00000000000042",
    "poNumber": "PO-1001",
    "assignmentId": "asg-001",
    "orderId": "ord-001",
    "packageNo": 1,
    "tier": "NEXT_DAY",
    "carrier": "RN-EXPRESS",
    "stopKey": "9f2a6c0d4b7e11aa5c3d8e90b1f47a62c8d0e3f15a9b7c24d6e8f0a1b3c5d7e9",
    "destinationPostalPrefix": "01310"
  }
}
```

## Consumed

Each topic has one consumer group (an environment variable, mandatory), a dead-letter topic `<topic>.dlq`, and an inbox row `(consumer, event_id)` in `processed_events` written in the same transaction as the effect. Unknown types and unknown fields are ignored. Handler: `internal/adapters/inbound/kafka/handlers.go`.

| Topic | Type | Effect |
| --- | --- | --- |
| `retailnetwork.vendor-connectivity.events` | `com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed` | one `SHIPMENT_CONFIRMED` scan per tracking number (the journey stays `LABELLED`) |
| `retailnetwork.linehaul-transportation.events` | `com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted` | `departureKind` `PICKUP` gives `PICKUP_DEPARTURE` (handover); other kinds give `DEPARTURE`; an absent kind promotes a `DEPARTURE` to `PICKUP_DEPARTURE` while the journey is still `LABELLED` |
| `retailnetwork.linehaul-transportation.events` | `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | node type `DELIVERY_STATION` gives `STATION_ARRIVAL`, anything else `TRANSIT_ARRIVAL` |
| `retailnetwork.last-mile-delivery.events` | `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | `OUT_FOR_DELIVERY` scan at `stationNodeId` |
| `retailnetwork.last-mile-delivery.events` | `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | outcome `DELIVERED` gives a `DELIVERED` scan, any other outcome `DELIVERY_FAILED` (back to `AT_STATION`) |
| `retailnetwork.last-mile-delivery.events` | `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` (the `route` spelling is accepted as an alias) | `ReturnToSender`: `AT_STATION` to `RETURNED`, raising `PackageReturned` |
| `retailnetwork.offer-availability.events` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | upsert of the local `catalog_dimensions` read model |

Scan ids of consumed events are `<CloudEvents id>:<trackingNumber>`, so a multi-package load event is idempotent per package. Events for tracking numbers this service did not label are counted as unknown and acknowledged.

### Failure behaviour

- A transient handler failure retries the same message with capped exponential backoff (initial 200 ms), then the message is dead-lettered to `<topic>.dlq` (`internal/adapters/inbound/kafka/kafka.go`, `deadletter.go`).
- A message that is not a valid CloudEvent, or a valid event that can never be applied, is skipped with a `WARN` (or dead-lettered for handled types), never retried.
- A `PackageReturnInitiated` for a journey that is not yet `AT_STATION` fails with an illegal-transition error and is retried, because the failed-attempt scan may still be on its way (ADR 0003).
