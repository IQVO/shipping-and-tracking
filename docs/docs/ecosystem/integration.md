---
id: integration
title: Integration
sidebar_label: Integration
description: "Every upstream and downstream of Shipping & Tracking: protocol, contract, failure behaviour, and the edges that do not exist."
---

# Integration

Shipping & Tracking integrates through published events, one in-organisation REST call in each direction and an unauthenticated read API. It never imports a sibling's Go packages: contracts are restated in `apis/asyncapi.yaml` and ADR 0003 and kept byte-identical by tests. The relationship patterns are drawn on the [context map](/docs/ddd/context-map).

## Upstream: what this context consumes or calls

| Upstream | Protocol | Contract | Failure behaviour |
| --- | --- | --- | --- |
| **customer-ordering** | Synchronous REST, `GET {CUSTOMER_ORDERING_URL}/orders/{orderId}/ship-to`, once per label, before the transaction opens | Response `name`, `line1`, optional `line2`, `city`, `region`, `postalCode`, `countryCode` (2 characters), optional `phone`; `internal/adapters/outbound/customerordering/gateway.go` | Bounded by `CUSTOMER_ORDERING_TIMEOUT` (3 s). 404 becomes a 422 `validation-failed` on `POST /labels` (unknown order); a timeout, transport error, other status or unreadable body becomes 503 `ship-to-unavailable` and no label is stored. Only label issuing degrades; void, scans, queries and all consumers keep working. Recovery is automatic and a retry with the same `Idempotency-Key` is safe. This is the only synchronous call the context makes |
| **vendor-connectivity** | Kafka `retailnetwork.vendor-connectivity.events` | `com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed`: `poNumber`, `nodeId`, `trackingNumbers[]`, `shippedAt` | One `SHIPMENT_CONFIRMED` scan per tracking number; the journey stays `LABELLED`. Retry then `.dlq` for transient failures |
| **linehaul-transportation** | Kafka `retailnetwork.linehaul-transportation.events` | `...load.LoadDeparted` (`originNodeId`, `trackingNumbers[]`, `departedAt`, `departureKind`), `...load.LoadArrived` (`destinationNodeId`, `trackingNumbers[]`, `arrivedAt`, `nodeType`) | Pickup departure is the handover; arrival at a delivery station is `STATION_ARRIVAL`. Unknown fields ignored |
| **last-mile-delivery** | Kafka `retailnetwork.last-mile-delivery.events` | `...route.PackageOutForDelivery`, `...route.DeliveryAttempted`, `...packageattempthistory.PackageReturnInitiated` (the `route` spelling is tolerated) | `PackageReturnInitiated` before the journey is `AT_STATION` is retried; closed or unknown journeys are acknowledged |
| **offer-availability** | Kafka `retailnetwork.offer-availability.events` | `...catalogitem.CatalogItemCreated` | Upserted into `catalog_dimensions` |

Failure behaviour common to the four Kafka consumers (`internal/adapters/inbound/kafka`): the consumer group is a mandatory variable; an offset commits only after the handler returned nil; a transient failure retries the same message with capped exponential backoff and then dead-letters it to `<topic>.dlq`; a message that is not a CloudEvent or a type the consumer does not act on is skipped; a handled type whose payload can never be applied is dead-lettered; a redelivered event id is skipped via `processed_events`. A Kafka outage never blocks boot (readers dial lazily) but makes `/readyz` answer 503. Topics are created by the first writer, so a silent upstream shows as consumer retry logs, not as a fault here.

## Downstream: who consumes or calls this context

| Downstream | Protocol | Contract | Failure behaviour |
| --- | --- | --- | --- |
| **vendor-connectivity** | REST `POST /labels` (in-organisation), `Idempotency-Key` required | `issueLabel` in `apis/openapi.yaml`; returns `labelRef`, `trackingNumber`, `carrier` among the label metadata | See the error table on the [API overview](/docs/api-reference). Retry with the same key replays the first answer for 24 h |
| **customer-ordering** | Kafka `LabelIssued`, `LabelVoided`, `PackageScanned`, `PackageDelivered`, `PackageExceptionRaised` into its order-tracking view; REST `GET /journeys/{trackingNumber}` | [Event catalogue](/docs/api-reference/events), `getJourney` | At-least-once with stable event ids; consumers dedupe on `id` |
| **linehaul-transportation** | Kafka `LabelIssued`, `LabelVoided` (expected volume) | as above | as above |
| **last-mile-delivery** | Kafka `LabelIssued` (groups stops by the opaque `stopKey`) | as above | as above |
| **delivery-promise** | Kafka `PackageDelivered`, `PackageExceptionRaised`, `PackageReturned`, `PackageLost` | as above | as above |
| **order-sourcing** | Kafka `PackageDelivered` | as above | as above |
| **node-performance** | Kafka `PackageHandedOver` | as above | as above |
| Shoppers, console, operators | REST `GET /journeys/{trackingNumber}`; the `shipping_mfe` console remote; MCP and A2A `get_journey` | OpenAPI, [MCP tools](/docs/mcp/tools), Agent Card | Unauthenticated; read-only; 404 `not-found` for an unknown tracking number |
| Agents | A2A `cmd/a2a`, skill `get_journey` | `/.well-known/agent-card.json` | Domain rejections are answered in the message envelope, never as JSON-RPC errors |

The consumer lists above come from the AsyncAPI descriptions of the published events; this repository does not verify that each consumer is actually deployed.

## Absent edges

- **No call to any sibling other than customer-ordering.** `cmd/mcp` and `cmd/a2a` make no REST, MCP or A2A call to any context and contain no outbound A2A client (`TestA2AHasNoOutboundClient`).
- **No node-topology edge.** No node event is consumed; node types come from the `nodeType` field of `LoadArrived`.
- **No carrier integration.** `carrier` is a label field only.
- **No fleet edge.** No `warehouse.*` topic and no call into the warehouse-systems fleet; a fleet node asks for a label through vendor-connectivity's Vendor API.
- **No analytics topic.** All eight events are integration events on `retailnetwork.shipping-and-tracking.events`; nothing is published on `retailnetwork.shipping-and-tracking.analytics`.
- **No consumer in this repository is known for `PackageScanned`** beyond customer-ordering's tracking view, and none of `PackageReturned` and `PackageLost` beyond delivery-promise, per the AsyncAPI.
- **A2A carries delegation only.** Facts between contexts travel exclusively as CloudEvents on Kafka.

## Edge exposure

In the local kind cluster, Kong (port 8000) exposes the API at `/api/shipping-and-tracking`, MCP at `/api/mcp/shipping-and-tracking` (prefix rewritten to `/mcp`) and A2A at `/api/a2a/shipping-and-tracking` (prefix rewritten to `/`); the console remote is served at `/mfes/shipping-and-tracking/` by the Nginx web gateway. Kafka is the platform's single broker.
