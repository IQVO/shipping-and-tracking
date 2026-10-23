# ADR 0002: the ShippingLabel and PackageJourney aggregates, their invariants and the assumptions the model left open

## Status

Accepted (2026-10-23). Assumptions A1 to A30 are final unless the orchestrator overrules them.

## Context

The model (retail-network-docs `model/model.yaml`, context `shipping-and-tracking`) names two
aggregates, `ShippingLabel` (root `labelRef`) and `PackageJourney` (root `trackingNumber`), and
leaves several things open (its Bounded Context Canvas lists nine open questions). The orchestrator's
`contracts-delta` (E1, E2, E3, E6, E10, E15, E16) and the Gap Register items G1, G2, G3, G6, G11, G16
and G18 resolve some of them; this ADR records the rest as numbered assumptions.

## Decision

### Packages

`internal/domain/label` (ShippingLabel), `internal/domain/journey` (PackageJourney, the richest
aggregate and the mutation focus) and `internal/domain/shared` (Event, Header, validation errors,
id and text checks). Pure Go: no I/O, the clock and the ids are arguments, every aggregate has
`Snapshot`/`Restore` and an optimistic `version`. The two aggregates share the tracking number as a
value, never as a reference.

### ShippingLabel

States `ISSUED`, `VOIDED`; commands `IssueLabel` (creates `ISSUED`, version 1, raises `LabelIssued`)
and `VoidLabel` (`ISSUED` to `VOIDED`, raises `LabelVoided`). The ship-to address is NOT a field of the
aggregate: it lives as an encrypted snapshot behind the `ShipToSnapshots` port (A9).

| Invariant | Error | Raised by | Problem type |
| --- | --- | --- | --- |
| one label per `(poNumber, packageNo)` | `ErrLabelAlreadyIssued` | repository (unique index) and a pre-check | `label-already-issued` 409 |
| `trackingNumber` is unique | `ErrTrackingNumberTaken` | repository (unique index) | `tracking-number-taken` 409 |
| a VOIDED label cannot be voided again with a new effect | none: the second void returns the label unchanged | use case | 200 |

### PackageJourney

States `LABELLED`, `HANDED_OVER`, `IN_TRANSIT`, `AT_STATION`, `OUT_FOR_DELIVERY`, `DELIVERED`,
`RETURNED`, `LOST` and `VOIDED` (A13). Terminal: `DELIVERED`, `RETURNED`, `LOST`, `VOIDED`. Entity
`Scan{scanId, nodeId, scanType, at, applied}`.

| Invariant | Error | Behaviour | Problem type |
| --- | --- | --- | --- |
| scans apply in time order; an older scan is stored, not applied | `ErrScanOutOfOrder` | the scan is kept with `applied=false`, the version moves, the state does not | `scan-out-of-order` 409 (REST); acknowledged by consumers |
| a `scanId` is applied once | `ErrDuplicateScan` | nothing is stored | `duplicate-scan` 409; acknowledged by consumers |
| `DELIVERED`, `RETURNED`, `LOST` (and `VOIDED`) are terminal | `ErrJourneyClosed` | every command is refused | `journey-closed` 409 |
| only the edges of the model exist | `ErrIllegalTransition` (shared) | the scan is kept with `applied=false` (A6) | `illegal-transition` 409 |
| a label is voided only before handover | `ErrVoidAfterHandover` (A13) | refused | `void-after-handover` 409 |

Transitions (model, plus the two additions of the Gap Register): `LABELLED to HANDED_OVER`,
`HANDED_OVER to IN_TRANSIT`, `IN_TRANSIT to IN_TRANSIT`, `IN_TRANSIT to AT_STATION`,
`AT_STATION to OUT_FOR_DELIVERY`, `OUT_FOR_DELIVERY to AT_STATION`, `OUT_FOR_DELIVERY to DELIVERED`
(all `RecordScan`), `AT_STATION to RETURNED` (`ReturnToSender`), `IN_TRANSIT to LOST` (`DeclareLost`);
added: `HANDED_OVER to AT_STATION` (direct injection, G3/E3) and `LABELLED to VOIDED` (G18/E16).
`RaiseException` records an exception in any open state and changes no state.

Events raised: `PackageScanned` for every applied scan; `PackageHandedOver` on `LABELLED to
HANDED_OVER`; `PackageDelivered` on `to DELIVERED`; `PackageExceptionRaised`; `PackageReturned`;
`PackageLost`. A handover or delivery scan raises two events in one transaction.

### Assumptions (numbered, final)

- **A1 (entity names).** The CloudEvents `<entity>` is the model's `label` and `journey`, not the aggregate
  names (`shippinglabel`). Published types are `...shipping-and-tracking.label.<Event>` and
  `...shipping-and-tracking.journey.<Event>`; the pack pages already use them.
- **A2 (identifiers).** `labelRef` is `lbl-<uuid>`; `trackingNumber` is `RN` followed by 16 uppercase
  base-32 characters from a crypto random source (valid ids: 1 to 64 characters of `A-Z a-z 0-9 . _ : -`).
  Both come from the `IDs` port. A tracking-number collision is retried once with a new number
  before `ErrTrackingNumberTaken` is answered.
- **A3 (carrier).** The carrier is chosen from the tier: `SAME_DAY` and `NEXT_DAY` give `RN-EXPRESS`,
  `TWO_DAY` and `STANDARD` give `RN-GROUND`.
- **A4 (StartJourney).** `StartJourney` runs inside `IssueLabel`, in the same transaction as the label, the
  snapshot and the `LabelIssued` outbox row. It raises no event of its own.
- **A5 (scan types and edges).** `scanType` is an enum, each value with fixed edges:
  `SHIPMENT_CONFIRMED` (`LABELLED` stays `LABELLED`), `PICKUP_DEPARTURE` (`LABELLED` to `HANDED_OVER`),
  `DEPARTURE` (`HANDED_OVER` or `IN_TRANSIT` to `IN_TRANSIT`), `TRANSIT_ARRIVAL` (`HANDED_OVER` or
  `IN_TRANSIT` to `IN_TRANSIT`), `STATION_ARRIVAL` (`HANDED_OVER` or `IN_TRANSIT` to `AT_STATION`),
  `OUT_FOR_DELIVERY` (`AT_STATION` to `OUT_FOR_DELIVERY`), `DELIVERED` (`OUT_FOR_DELIVERY` to `DELIVERED`),
  `DELIVERY_FAILED` (`OUT_FOR_DELIVERY` to `AT_STATION`). `VendorShipmentConfirmed` is an applied scan
  that moves no state and raises `PackageScanned`.
- **A6 (a scan without an edge).** A scan whose type has no edge from the current state is stored with
  `applied=false`, raises no event and answers `ErrIllegalTransition` (409 `illegal-transition`); a
  consumer acknowledges it. The model names no error for it.
- **A7 (ExceptionOnSilence).** The model gives this context no expected leg time. The sweep measures
  silence from the last applied scan, for open journeys past `LABELLED`, against a window by tier for
  `HANDED_OVER`, `IN_TRANSIT` and `AT_STATION` (`SAME_DAY` 6 h, `NEXT_DAY` 24 h, `TWO_DAY` 48 h,
  `STANDARD` 72 h) and 12 h for `OUT_FOR_DELIVERY`. The journey records `lastExceptionAt`; the sweep raises
  one `NO_SCAN` exception per silence (skipped until a new applied scan arrives). The sweep interval is
  `SILENCE_SWEEP_INTERVAL` (default 5 minutes).
- **A8 (DeclareLost, ReturnToSender triggers).** `DeclareLost` has no trigger in the model. The sweep
  declares an `IN_TRANSIT` journey lost when its `NO_SCAN` exception has stayed unanswered for 48 hours
  (no applied scan since). `ReturnToSender` is triggered by last-mile-delivery's `PackageReturnInitiated`
  (E15) on an `AT_STATION` journey; `returnedAt` is the event's CloudEvents `time`.
- **A9 (ship-to snapshot).** The ship-to read from customer-ordering is encrypted (AES-256-GCM, a fresh
  nonce per row, key from the mandatory `SHIPTO_ENCRYPTION_KEY`, 32 bytes base64) into table
  `ship_to_snapshots(label_ref, nonce, ciphertext, key_id, created_at)` and never returned or published.
  It is purged 30 days after the journey closed (`DELIVERED`, `RETURNED`, `LOST` or `VOIDED`): a purge
  sweep deletes snapshots whose journey `closed_at` is older than 30 days.
- **A10 (stopKey and postal prefix).** `stopKey` = hex HMAC-SHA-256, keyed by the mandatory
  `STOP_KEY_SECRET`, over the normalized `line1|line2|city|region|postalCode|countryCode` (upper case, only
  letters and digits, whitespace and punctuation dropped; the recipient name and phone are NOT part of
  it, so neighbours at one address share a stop). `destinationPostalPrefix` is the first 5 letters or digits
  of the normalized postal code (the whole code when shorter).
- **A11 (GetShipTo client).** `CUSTOMER_ORDERING_URL` is mandatory. The call is made before the
  transaction opens, with a 3 second timeout and no retry. A 404 answers 422 `validation-failed` (the order
  is unknown); a transport error, timeout or any other status answers 503 `ship-to-unavailable`; nothing
  is stored.
- **A12 (LabelIssued extra).** `LabelIssued` carries `orderId` in addition to the model's fields and
  `stopKey` / `destinationPostalPrefix` (E10). It is an additive identifier, not PII; customer-ordering needs it
  to attach the tracking number to an order. Reported to the orchestrator.
- **A13 (VoidLabel, G18/E16).** Voiding moves the label to `VOIDED` and closes a `LABELLED` journey as
  `VOIDED` in one transaction. A journey past `LABELLED` refuses with `ErrVoidAfterHandover`. Voiding a
  `VOIDED` label returns it unchanged, publishes nothing and is 200.
- **A14 (shared problem types).** A missing label or journey is the shared `not-found` (404), not
  `label-not-found` / `journey-not-found` (conventions.errors, decision D3).
- **A15 (concurrency).** Each aggregate carries `version`; a save expects `stored = version - 1` and a
  mismatch is `ErrConcurrentUpdate` (409). Consumers and the sweep reload and retry up to 3 times.
- **A16 (multi-package events).** One `LoadDeparted` / `LoadArrived` / `VendorShipmentConfirmed` names many
  tracking numbers. Each becomes one scan with `scanId = <CloudEvents id>:<trackingNumber>`. The whole event
  is handled in ONE transaction that claims the CloudEvents `id` in `processed_events` first and then
  applies every scan; an `ErrConcurrentUpdate` retries the whole event. A redelivery after a crash finds the
  claim and skips; `ErrDuplicateScan` per scan is the second line of defence.
- **A17 (unknown tracking number).** A consumed fact for a tracking number with no journey is logged and
  counted, then acknowledged (the package may belong to a flow this service did not label).
- **A18 (CatalogDimensionsView).** `CatalogItemCreated` is upserted into `catalog_dimensions`; no command
  or route reads it yet (the model names no reader). It is kept so a printable label can use it later.
- **A19 (restricted label route).** G11 mentions a restricted route through which partners resolve the
  address; contracts-delta E10 says no address leaves this service. It is NOT built and is listed as a model gap.
- **A20 (scan direction facts).** Event mapping (details in ADR 0003): `LoadDeparted` kind `PICKUP` is
  `PICKUP_DEPARTURE`, any other kind `DEPARTURE`; an absent kind is `PICKUP_DEPARTURE` for a `LABELLED`
  journey and `DEPARTURE` otherwise. `LoadArrived` with node type `DELIVERY_STATION` is `STATION_ARRIVAL`,
  any other or absent type `TRANSIT_ARRIVAL`. `DeliveryAttempted` outcome `DELIVERED` is `DELIVERED`, any
  other outcome `DELIVERY_FAILED`.
- **A21 (node of a delivery scan).** `DeliveryAttempted` carries no node; its scan takes the node of the
  journey's last applied scan (the station of the `OUT_FOR_DELIVERY` scan).
- **A22 (times).** The scan instant is the event's own field (`departedAt`, `arrivedAt`, `shippedAt`, `at`).
  Equal instants are in order (a scan is out of order only when strictly older than the last applied one).
- **A23 (scanId).** An event-derived `scanId` (A16) makes redelivery idempotent; REST callers choose their
  own `scanId`, unique per journey.
- **A24 (idempotency keys).** `idempotency_keys(route, key)` with a 24 hour TTL, replayed on the same
  body; a key reused with another body answers 422 `validation-failed`; a missing key answers 400
  `idempotency-key-required` (the fleet rule).
- **A25 (bounds).** `packageNo` is 1 to 2147483647; texts are bounded (ids 128, reason 500).
- **A26 (journey tier).** The journey copies `tier` from the label so the sweep needs no join.
- **A27 (handover fields).** `PackageHandedOver.nodeId` is the origin node of the pickup departure and
  `handedOverAt` its `departedAt`.
- **A28 (analytics).** No event is analytics-only (E6, E16 promoted the three that were); no
  `retailnetwork.shipping-and-tracking.analytics` message is written.
- **A29 (no PII in logs or errors).** Errors and logs carry ids, never the address or the recipient.
- **A30 (PackageExceptionRaised).** The only reason is `NO_SCAN`; a failed delivery attempt is not an
  exception (last-mile-delivery publishes its own risk events, E15).

## Consequences

- The model's `Err` names appear verbatim as Go errors in `internal/domain/label` and
  `internal/domain/journey` and as problem slugs on the wire.
- The model gaps this ADR leaves for the orchestrator (A7, A8, A19, A12) are listed in the final report.
