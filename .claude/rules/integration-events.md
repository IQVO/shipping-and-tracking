---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

This service PUBLISHES and CONSUMES. The retail-network namespace overrides the fleet's `warehouse` one (see
`fleet/cloudevents.md`): topics `retailnetwork.<context>.events`, `source=/retailnetwork/shipping-and-tracking`,
`type=com.retailnetwork.logistics.shipping-and-tracking.<entity>.<Event>`, `dataschema=urn:retailnetwork:shipping-and-tracking:events:<Event>:v1`.

- Publishes (all eight on `retailnetwork.shipping-and-tracking.events`, key = subject): `label.LabelIssued`, `label.LabelVoided`, `journey.PackageScanned`, `journey.PackageHandedOver`, `journey.PackageDelivered`, `journey.PackageExceptionRaised`, `journey.PackageReturned`, `journey.PackageLost`. The encoder is `internal/adapters/outbound/kafka/encoder.go`; the catalogue is ADR 0004; the contract is `apis/asyncapi.yaml`.
- Consumes (one consumer and one mandatory `*_CONSUMER_GROUP` per topic): vendor-connectivity `VendorShipmentConfirmed`, linehaul-transportation `LoadDeparted` / `LoadArrived`, last-mile-delivery `PackageOutForDelivery` / `DeliveryAttempted` / `PackageReturnInitiated`, offer-availability `CatalogItemCreated` (ADR 0003, `internal/adapters/inbound/kafka`).
- Poison messages go to `<topic>.dlq` at once; transient failures retry five times with backoff, then go there too. The inbox (`processed_events`) makes redelivery harmless.
- `TestEventCatalogueMatchesContract` and `TestConsumedTypesAreDocumented` keep the contract, the ADRs and the code together.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`retailnetwork.<ctx>.events` AND analytics `retailnetwork.<ctx>.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` — copy it from this template's
  `templates/cloudevents/cloudevents.go.tmpl` (+ its test) and change only
  the per-repo constants. Transport stays `segmentio/kafka-go` (no sdk-go
  protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/retailnetwork/<repo>`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:<repo>:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.retailnetwork.<tier>.<bounded-context>.<entity>.<EventName>`
  (this repo: `com.retailnetwork.logistics.shipping-and-tracking.<label|journey>.<EventName>`). The SAME `type` names the
  occurrence on both the integration and the analytics topic; `dataschema`
  names the payload shape. Breaking payload change => new `.v2` type + new
  dataschema version, never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via
  `DataAs`, dedupe on `id`, and DLQ/skip — WARN log + commit past, never
  crash, never block the partition, never fall back to parsing a legacy
  shape — anything that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes +
  the `content-type` header); a legacy-flat-message-rejected test per
  consumer; Kafka integration tests via testcontainers only.

Record: ADR 0004 (`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`) is this repo's catalogue.

### Published types

All on `retailnetwork.shipping-and-tracking.events`, `dataschema` = `urn:retailnetwork:shipping-and-tracking:events:<EventName>:v1`.

| `type` | `subject` |
| --- | --- |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `labelRef` |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided` | `labelRef` |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageScanned` | `trackingNumber` |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedOver` | `trackingNumber` |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered` | `trackingNumber` |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageExceptionRaised` | `trackingNumber` |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageReturned` | `trackingNumber` |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost` | `trackingNumber` |

### Consumed types

The exact strings are the constants of `internal/adapters/inbound/kafka/handlers.go` (also ADR 0003).

| `type` | topic | producer |
| --- | --- | --- |
| `com.retailnetwork.partner.vendor-connectivity.purchaseorder.VendorShipmentConfirmed` | `retailnetwork.vendor-connectivity.events` | vendor-connectivity |
| `com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery` | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted` | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated` (also `...route.PackageReturnInitiated`) | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `retailnetwork.offer-availability.events` | offer-availability |

## Consumer group id

The consumer group id MUST be env-configurable, never a hardcoded string literal --
`internal/architecture/fitness_test.go`'s
TestKafkaConsumerGroupNeverHardcodedInline enforces this (a real incident:
wes-work-planning's hardcoded group id let a locally-run e2e-tests process
silently collide with the live in-cluster Deployment's consumer group on
the shared fleet Kafka broker).

Here the groups come from `VENDOR_CONSUMER_GROUP`, `LINEHAUL_CONSUMER_GROUP`, `LASTMILE_CONSUMER_GROUP` and
`CATALOG_CONSUMER_GROUP`, all mandatory (a missing one is a boot error naming it). The consumers resume from the
committed offset; none replays from the first offset.
