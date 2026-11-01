---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every application use case of Shipping & Tracking: trigger, inputs, invariants, events raised, errors and the problem types they become."
---

# Use cases

The use cases live in `internal/application/usecases`. Every use case that changes an aggregate saves it and hands its events to the outbox inside **one Postgres unit of work** (`writer.transact` in `writer.go`); the relay publishes the outbox rows to `retailnetwork.shipping-and-tracking.events`. Consumed events add their CloudEvents `id` to `processed_events` in the same transaction as the effect. The one synchronous call, `GetShipTo`, is made **before** the transaction opens, so no lock is held across the network.

## Triggers at a glance

| Use case | Trigger | Inbound adapter | Events raised |
| --- | --- | --- | --- |
| `IssueLabel` (includes `StartJourney`) | `vendor-connectivity` | REST `POST /labels` | `LabelIssued` |
| `VoidLabel` | operations tooling | REST `POST /labels/{labelRef}:void` | `LabelVoided` |
| `RecordScan` | any REST caller | REST `POST /journeys/{trackingNumber}/scans` | `PackageScanned`, plus `PackageHandedOver` or `PackageDelivered` on those transitions |
| `ApplyScans` | consumed `VendorShipmentConfirmed`, `LoadDeparted`, `LoadArrived`, `PackageOutForDelivery`, `DeliveryAttempted` | the four Kafka consumers | same as `RecordScan`, per tracking number |
| `ReturnToSender` | consumed `PackageReturnInitiated` | last-mile consumer | `PackageReturned` |
| `SweepSilence` (`RaiseException`, `DeclareLost`) | timer, `SILENCE_SWEEP_INTERVAL` (5 m), also at start | scheduler job `silence-sweep` | `PackageExceptionRaised`, `PackageLost` |
| `PurgeShipTo` | timer, `HOUSEKEEPING_INTERVAL` (1 h), also at start | scheduler job `ship-to-purge` | none |
| `Housekeeping` of the technical tables | timer, `HOUSEKEEPING_INTERVAL`, also at start | scheduler job `housekeeping` (`postgres.Housekeeping`) | none |
| `RecordCatalogItem` | consumed `CatalogItemCreated` | catalog consumer | none |
| `GetJourney` (query) | shoppers, customer-ordering, console, agents | REST `GET /journeys/{trackingNumber}`, MCP and A2A `get_journey` | none |

## Errors and the problem types they become

REST errors are RFC 9457 `application/problem+json`; `type` is the slug (`internal/adapters/inbound/http/errors.go`).

| Error | HTTP | Slug |
| --- | --- | --- |
| `shared.ErrValidation` and malformed bodies | 422 | `validation-failed` |
| missing `Idempotency-Key` | 400 | `idempotency-key-required` |
| `ports.ErrNotFound` | 404 | `not-found` |
| `label.ErrLabelAlreadyIssued` | 409 | `label-already-issued` |
| `label.ErrTrackingNumberTaken` | 409 | `tracking-number-taken` |
| `journey.ErrVoidAfterHandover` | 409 | `void-after-handover` |
| `journey.ErrDuplicateScan` | 409 | `duplicate-scan` |
| `journey.ErrScanOutOfOrder` | 409 | `scan-out-of-order` |
| `journey.ErrJourneyClosed` | 409 | `journey-closed` |
| `shared.ErrIllegalTransition` | 409 | `illegal-transition` |
| `ports.ErrConcurrentUpdate` | 409 | `concurrent-update` |
| `ports.ErrShipToUnavailable` | 503 | `ship-to-unavailable` |
| anything else | 500 | `internal-error` |

## IssueLabel

Source: `internal/application/usecases/labels.go`, `internal/domain/label/label.go`.

- **Trigger:** `vendor-connectivity` while handling a vendor's label request. REST `POST /labels`, `Idempotency-Key` required.
- **Input:** `poNumber`, `packageNo` (1 to 2147483647), `assignmentId`, `orderId`, `tier` (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`); ids are 1 to 64 characters.
- **Invariants:** one label per `(poNumber, packageNo)` (`ErrLabelAlreadyIssued`); a tracking number is unique network-wide (`ErrTrackingNumberTaken`, one retry with a fresh number before the error surfaces); the carrier follows the tier (`RN-EXPRESS` for `SAME_DAY` and `NEXT_DAY`, `RN-GROUND` otherwise); the `stopKey` is 64 lower-case hex characters.
- **Flow:**
  1. Validate the command with placeholder values; reject with `validation-failed`.
  2. Look up a label for `(poNumber, packageNo)`; a hit is `label-already-issued`.
  3. Call `GetShipTo` on customer-ordering. A 404 becomes `validation-failed` ("orderId ... is unknown to customer-ordering"); any other failure is `ship-to-unavailable`.
  4. Validate the address, seal it (AES-256-GCM, fresh nonce), derive `stopKey` (HMAC-SHA-256 of the normalised address) and the five-character postal prefix.
  5. In one transaction: mint `labelRef` (`lbl-<uuid>`) and a tracking number (`RN` plus 16 base-32 characters), save the label, save the sealed snapshot, **start the journey** (`StartJourney`, state `LABELLED`, version 1, no event of its own) and enqueue `LabelIssued`.
- **Result:** `201` with the label metadata, never the address. The attempt is counted once in `shipping.labels.issued` as `issued` or `rejected` with the reason.
- **Idempotency:** the `Idempotency-Key` replays the first response; a new key for the same package hits `label-already-issued`.

## VoidLabel

- **Trigger:** operations tooling. REST `POST /labels/{labelRef}:void`, body `reason` (1 to 500 characters), `Idempotency-Key` required.
- **Invariants:** a label can be voided only while its journey is still `LABELLED` (`ErrVoidAfterHandover` once handed over); a `VOIDED` label is returned unchanged and nothing is published.
- **Flow:** load the label, load the journey of the same tracking number, close the journey as `VOIDED` (no journey event), move the label `ISSUED` to `VOIDED` with the reason, save both, enqueue `LabelVoided`. `200` with the label.
- **Errors:** `not-found`, `void-after-handover`, `journey-closed`, `validation-failed`.

## RecordScan (REST) and ApplyScans (events)

Sources: `usecases/scans.go`, `internal/domain/journey/journey.go`, `internal/adapters/inbound/kafka/handlers.go`.

- **Input (REST):** `scanId` (1 to 256), `nodeId` (1 to 64), `scanType`, `at` (RFC 3339), path `trackingNumber`. **Input (events):** one `Fact` per tracking number of the event; the scan id is `<CloudEvents id>:<trackingNumber>` and a missing node takes the node of the journey's last applied scan (`unknown` when none).
- **Edge table** (`journey.next`): `SHIPMENT_CONFIRMED` keeps `LABELLED`; `PICKUP_DEPARTURE` takes `LABELLED` to `HANDED_OVER`; `DEPARTURE` and `TRANSIT_ARRIVAL` take `HANDED_OVER` or `IN_TRANSIT` to `IN_TRANSIT`; `STATION_ARRIVAL` takes `HANDED_OVER` or `IN_TRANSIT` to `AT_STATION`; `OUT_FOR_DELIVERY` takes `AT_STATION` to `OUT_FOR_DELIVERY`; `DELIVERED` takes `OUT_FOR_DELIVERY` to `DELIVERED`; `DELIVERY_FAILED` takes `OUT_FOR_DELIVERY` back to `AT_STATION`.
- **Order of checks** in `Journey.RecordScan`: validation, `ErrJourneyClosed`, `ErrDuplicateScan` (nothing stored), `ErrScanOutOfOrder` (stored with `applied=false`), the edge table (`ErrIllegalTransition`, stored with `applied=false`).
- **Events:** every applied scan raises `PackageScanned`; `LABELLED` to `HANDED_OVER` also raises `PackageHandedOver` (`HandoverOnFirstScan`); a move to `DELIVERED` also raises `PackageDelivered` and closes the journey.
- **REST outcome:** `200` with the journey, or the 409 above; a scan stored unapplied is committed and the caller still gets the error. **Consumer outcome:** `ApplyScans` claims the event id in the inbox first (a replay is `skipped`), then folds every fact; duplicate, out-of-order, no-edge and closed outcomes are counted and acknowledged, an unknown tracking number is counted and acknowledged, anything else is returned and retried.

## ReturnToSender

- **Trigger:** consumed `PackageReturnInitiated` (last-mile-delivery, third failed attempt).
- **Invariant:** only an `AT_STATION` journey can be returned. A closed journey and an unknown tracking number are acknowledged; a journey not yet `AT_STATION` returns `ErrIllegalTransition`, which the consumer retries because the failed-attempt scan may still be on its way.
- **Flow:** claim the event, move `AT_STATION` to `RETURNED`, enqueue `PackageReturned` (`returnedAt` from the event).

## SweepSilence: RaiseException and DeclareLost

Source: `usecases/sweep.go`, `internal/domain/journey/types.go`.

- **Trigger:** the `silence-sweep` job. Candidate journeys come from `ListStale` in pages of 200 (`SweepPageSize`), each decided in its own unit.
- **Silence windows** while `HANDED_OVER`, `IN_TRANSIT` or `AT_STATION`: 6 h `SAME_DAY`, 24 h `NEXT_DAY`, 48 h `TWO_DAY`, 72 h `STANDARD`; 12 h while `OUT_FOR_DELIVERY`; `LABELLED` and terminal states are not watched.
- **RaiseException:** a watched journey past its window with no unanswered exception gets `PackageExceptionRaised` (reason `NO_SCAN`, the only reason); the state does not change; one exception per silence, a new applied scan re-arms it.
- **DeclareLost:** an `IN_TRANSIT` journey whose exception stayed unanswered for 48 h moves to `LOST` and enqueues `PackageLost`.
- **Result:** the job logs `raised=<n> lost=<n> conflicts=<n>`; a concurrent update is counted as a conflict and seen again on the next tick.

## PurgeShipTo and housekeeping

- `PurgeShipTo` deletes `ship_to_snapshots` whose journey closed more than 30 days ago (`ShipToRetention`; ADR 0002 A9) and logs `snapshots_deleted=<n>`.
- Housekeeping (`cmd/api/main.go` `housekeep`) deletes `idempotency_keys` older than 24 h, already-relayed `outbox_events` older than 7 days and `processed_events` older than 30 days.

## RecordCatalogItem

On a consumed `CatalogItemCreated`, validate the `catalogItemId`, claim the event id and upsert `catalog_dimensions` (title, dimensions, weight, `hazmat`, `fragile`). Nothing is published.

## GetJourney (query)

Loads the `PackageJourney` through the repository and returns the view: state, label identity (`poNumber`, `assignmentId`, `orderId`, `tier`), `handedOverAt`, `deliveredAt` and every stored scan in recorded order. Unknown tracking number: `ErrNotFound`. REST, MCP and A2A all call this one use case.
