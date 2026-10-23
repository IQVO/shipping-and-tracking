# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-23).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (retail-network-docs `model/model.yaml` `conventions.cloudevents`; the repo
rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every
type shipping-and-tracking publishes, followed by the types it consumes.
`TestEventCatalogueMatchesContract` checks the published list against
`apis/asyncapi.yaml`.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source`
  `/retailnetwork/shipping-and-tracking`; `subject` the aggregate root id
  (`labelRef` for label events, `trackingNumber` for journey events); `time` the
  instant the change was committed, in UTC; `datacontenttype` `application/json`;
  `dataschema` `urn:retailnetwork:shipping-and-tracking:events:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one aggregate stay ordered on one partition.
- `data` is camelCase; optional fields are omitted when unset; arrays are always
  present; timestamps are RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the entity the model names for the event: `label` (raised by the
  ShippingLabel aggregate) and `journey` (raised by the PackageJourney aggregate).
  Tier is `logistics`. See ADR 0002 A1.
- No `data` of any event carries a ship-to address or a part of it. `stopKey` (an
  opaque keyed hash) and `destinationPostalPrefix` on `LabelIssued` are the only
  values derived from the address (ADR 0002 A9, A10).

### Published types

Every type below goes to the topic `retailnetwork.shipping-and-tracking.events`.
`LabelVoided`, `PackageReturned` and `PackageLost` were analytics-only in the
model; contracts-delta E6 and E16 promote them to integration events, so nothing
is written to `retailnetwork.shipping-and-tracking.analytics`.

```text
type: com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.journey.PackageScanned stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.journey.PackageHandedOver stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.journey.PackageExceptionRaised stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.journey.PackageReturned stream: events
type: com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost stream: events
```

### Consumed types

Dispatch is on the full type string (ADR 0003). The consumed catalogue is in
ADR 0003; the AsyncAPI file carries one `receive` operation per producer topic.

## Consequences

- A new published event needs, in one commit: the encoder, its golden JSON test,
  the AsyncAPI message and a `type:` line here.
- A payload change is additive. A breaking change is a new `.v2` type.
