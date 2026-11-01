---
id: domain-events
title: Domain events
sidebar_label: Domain events
description: "Every event Shipping & Tracking publishes and consumes: full CloudEvents types, topics, keys, data fields, producing use cases, consumers and idempotency."
---

# Domain events

Every event this context publishes and consumes, from `internal/adapters/outbound/kafka/encoder.go` (payloads), `internal/adapters/inbound/kafka/handlers.go` (consumed payloads) and the contract `apis/asyncapi.yaml`. The catalogue with failure behaviour is on [Event catalogue](/docs/api-reference/events); the domain-wide rules are on the central [event standard](https://iqvo.github.io/warehouse-docs/network/strategic-design/event-standard-cloudevents).

## Envelope

Every Kafka message is a CloudEvents 1.0 event in structured JSON mode, built only by `internal/adapters/kafka/cloudevents/cloudevents.go`.

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | A UUID minted once when the outbox row is written (`outbox_events.event_id`): a relay retry re-sends the same `id` |
| `source` | `/retailnetwork/shipping-and-tracking` |
| `type` | `com.retailnetwork.logistics.shipping-and-tracking.<entity>.<EventName>`, entity `label` or `journey` |
| `subject` | The aggregate root id: `labelRef` for `label` events, `trackingNumber` for `journey` events |
| `time` | The instant of the change from the service clock (`OccurredAt`), UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:shipping-and-tracking:events:<EventName>:v1` |
| Kafka topic | `retailnetwork.shipping-and-tracking.events` |
| Kafka key | Same as `subject`, so one journey's events stay in order on one partition |

Every event leaves through the transactional outbox, in the same transaction as the aggregate change. No event carries a ship-to address or any part of it. Payload timestamps are RFC 3339 UTC strings; `data` is camelCase.

## Published

| Event | Full `type` | Subject and key | Producing use case | Consumers (per the AsyncAPI) |
| --- | --- | --- | --- | --- |
| `LabelIssued` | `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `labelRef` | `IssueLabel` | linehaul-transportation, customer-ordering, last-mile-delivery |
| `LabelVoided` | `...label.LabelVoided` | `labelRef` | `VoidLabel` | customer-ordering, linehaul-transportation |
| `PackageScanned` | `...journey.PackageScanned` | `trackingNumber` | `RecordScan`, `ApplyScans` (every applied scan) | none named |
| `PackageHandedOver` | `...journey.PackageHandedOver` | `trackingNumber` | a scan taking `LABELLED` to `HANDED_OVER` | node-performance |
| `PackageDelivered` | `...journey.PackageDelivered` | `trackingNumber` | a scan taking `OUT_FOR_DELIVERY` to `DELIVERED` | delivery-promise, order-sourcing, customer-ordering |
| `PackageExceptionRaised` | `...journey.PackageExceptionRaised` | `trackingNumber` | `SweepSilence` (`RaiseException`) | delivery-promise, customer-ordering |
| `PackageReturned` | `...journey.PackageReturned` | `trackingNumber` | `ReturnToSender` | delivery-promise |
| `PackageLost` | `...journey.PackageLost` | `trackingNumber` | `SweepSilence` (`DeclareLost`) | delivery-promise |

The consumer column comes from the AsyncAPI descriptions; this repository does not verify that each consumer is deployed. In the table `...` abbreviates `com.retailnetwork.logistics.shipping-and-tracking`.

### LabelIssued

Source: `internal/domain/label/events.go`, `labelIssuedData`.

| Field | Type | Meaning |
| --- | --- | --- |
| `labelRef` | string | Identity of the label |
| `trackingNumber` | string | The package's network-wide unique id |
| `poNumber` | string | The purchase order the package belongs to |
| `assignmentId` | string | The fulfillment assignment behind the purchase order |
| `orderId` | string | The customer order (additive field, ADR 0002 A12) |
| `packageNo` | integer | Package number within the purchase order |
| `tier` | enum `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD` | Service tier printed on the label |
| `carrier` | string | `RN-EXPRESS` or `RN-GROUND` |
| `stopKey` | string | Opaque HMAC-SHA-256 of the normalised ship-to address, 64 lower-case hex characters. The address itself never leaves this context |
| `destinationPostalPrefix` | string | The first five letters or digits of the normalised postal code |

### LabelVoided

| Field | Type | Meaning |
| --- | --- | --- |
| `labelRef` | string | Identity of the label |
| `trackingNumber` | string | Tracking number of the voided label |
| `reason` | string | Why it was voided, 1 to 500 characters |

### PackageScanned

Source: `internal/domain/journey/events.go`, `packageScannedData`.

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | The package |
| `scanId` | string | Identity of the applied scan |
| `nodeId` | string | Where the package was observed |
| `scanType` | enum, the eight scan types | Kind of observation |
| `at` | timestamp | When it was observed (the scan's own time) |
| `state` | enum, the nine journey states | Journey state after the scan |

### PackageHandedOver

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | The package |
| `poNumber` | string | The purchase order, so node-performance can compare with its required ship-by |
| `nodeId` | string | The vendor node it left (origin of the pickup departure) |
| `handedOverAt` | timestamp | The handover instant |

### PackageDelivered

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | The package |
| `assignmentId` | string | The assignment, for order-sourcing |
| `orderId` | string | The customer order |
| `deliveredAt` | timestamp | Time of the successful attempt |

### PackageExceptionRaised

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | The package |
| `assignmentId` | string | The assignment |
| `orderId` | string | The customer order |
| `reason` | string | `NO_SCAN`, the only reason |
| `at` | timestamp | When the exception was raised (the sweep's clock) |

### PackageReturned

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | The package |
| `orderId` | string | The customer order |
| `returnedAt` | timestamp | The `at` of the consumed `PackageReturnInitiated` |

### PackageLost

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | The package |
| `orderId` | string | The customer order |
| `declaredAt` | timestamp | When the sweep declared the package lost |

### Example envelope: PackageDelivered

`PackageDelivered` closes the journey and is what delivery-promise and order-sourcing react to. The values are synthetic.

```json
{
  "specversion": "1.0",
  "id": "5b6f2a0e-8d1c-4f7a-9c3e-2a41d7e9b0c4",
  "source": "/retailnetwork/shipping-and-tracking",
  "type": "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered",
  "subject": "RN7K3Q2M4B6D5F2XAB",
  "time": "2026-10-22T17:42:12Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:shipping-and-tracking:events:PackageDelivered:v1",
  "data": {
    "trackingNumber": "RN7K3Q2M4B6D5F2XAB",
    "assignmentId": "asg-7f3c21",
    "orderId": "ord-20261021-0091",
    "deliveredAt": "2026-10-22T17:42:10Z"
  }
}
```

## Consumed

Source: `internal/adapters/inbound/kafka/handlers.go`. Consumers dispatch on the full `type` string, never on a suffix; unknown types and unknown fields are ignored.

| Producer | Full `type` | Topic | Fields used | What this context does |
| --- | --- | --- | --- | --- |
| vendor-connectivity | `com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed` | `retailnetwork.vendor-connectivity.events` | `poNumber`, `nodeId`, `trackingNumbers`, `shippedAt` | One `SHIPMENT_CONFIRMED` scan per tracking number at `nodeId`. It is applied and raises `PackageScanned`, but a `LABELLED` journey stays `LABELLED` (handover comes from the pickup scan) |
| linehaul-transportation | `com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted` | `retailnetwork.linehaul-transportation.events` | `originNodeId`, `trackingNumbers`, `departedAt`, `departureKind` | One scan per tracking number at `originNodeId`. Kind `PICKUP` (or no kind on a `LABELLED` journey) is `PICKUP_DEPARTURE`, the handover. Any other kind is `DEPARTURE` |
| linehaul-transportation | `...load.LoadArrived` | same topic | `destinationNodeId`, `trackingNumbers`, `arrivedAt`, `nodeType` (or `destinationNodeType`) | `STATION_ARRIVAL` when the node type is `DELIVERY_STATION`, otherwise `TRANSIT_ARRIVAL` |
| last-mile-delivery | `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | `retailnetwork.last-mile-delivery.events` | `trackingNumber`, `stationNodeId`, `at` | `OUT_FOR_DELIVERY` scan at `stationNodeId` |
| last-mile-delivery | `...route.DeliveryAttempted` | same topic | `trackingNumber`, `outcome`, `at` | Outcome `DELIVERED` gives a `DELIVERED` scan, any other outcome `DELIVERY_FAILED`. The event has no node id: the scan takes the node of the journey's last applied scan |
| last-mile-delivery | `...packageattempthistory.PackageReturnInitiated` (the `route` spelling is accepted) | same topic | `trackingNumber`, `at` | `ReturnToSender`: `AT_STATION` to `RETURNED`, raising `PackageReturned` |
| offer-availability | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `retailnetwork.offer-availability.events` | `catalogItemId`, `title`, `lengthMm`, `widthMm`, `heightMm`, `weightG`, `hazmat`, `fragile` | Upsert of `catalog_dimensions` |

### Idempotency

- **Inbox.** `ApplyScans` and `ReturnToSender` claim `(consumer, CloudEvents id)` in `processed_events` first, in the same transaction as every journey change of the event. A message whose id is already claimed is acknowledged and skipped. `RecordCatalogItem` claims the same way.
- **Per-scan dedupe.** One load event names many tracking numbers. The scan id is `<CloudEvents id>:<trackingNumber>`, so a scan is applied once per package even if the claim were lost. A duplicate scan is counted and acknowledged, never retried.
- **Out of order and no edge.** Stored with `applied=false`, counted, acknowledged.
- **Closed journeys.** Counted and acknowledged; a scan after delivery is a producer-side anomaly, not a retry case.
- **Unknown tracking number.** One this context never issued is counted and acknowledged. There is no dead-letter for it.
- **Read model.** `catalog_dimensions` is an upsert on `catalogItemId` and is naturally idempotent.
- **Dead letters.** A transient failure retries with capped backoff and is then written to `<topic>.dlq`; a handled type whose payload can never be applied is dead-lettered at once.
