---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
description: "UML sequence diagrams for every Shipping & Tracking command use case, from inbound adapter to Kafka through the transactional outbox."
---

# Sequence diagrams

One sequence diagram per command use case of [Use cases](/docs/ddd/use-cases). To stay readable, `DB` stands for one Postgres transaction that holds the repositories, the `outbox_events` insert and, for consumers, the `processed_events` insert. `Relay` stands for the outbox relay (`internal/adapters/outbound/outbox/relay.go`) that publishes committed rows to the Kafka topic `retailnetwork.shipping-and-tracking.events`. The relay always runs after the commit, never inside it.

## 1. IssueLabel (with Start journey)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http adapter
    participant UC as IssueLabel
    participant CO as customer-ordering
    participant DB as DB transaction
    participant RL as Relay to Kafka
    Note over H: vendor-connectivity calls POST /labels with an Idempotency-Key
    H->>DB: find the Idempotency-Key
    alt key seen with the same body
        DB-->>H: stored response
        Note over H: replay the first answer, nothing else runs
    else new key
        H->>UC: Execute poNumber, packageNo, assignmentId, orderId, tier
        UC->>UC: validate the command
        UC->>DB: find label by poNumber and packageNo
        alt label exists
            UC-->>H: ErrLabelAlreadyIssued
            Note over H: 409 label-already-issued
        else no label
            UC->>CO: GetShipTo, GET /orders/orderId/ship-to
            alt 404
                UC-->>H: orderId unknown
                Note over H: 422 validation-failed
            else timeout or other failure
                UC-->>H: ErrShipToUnavailable
                Note over H: 503 ship-to-unavailable, nothing stored
            else ship-to returned
                UC->>UC: validate address, seal it, derive stopKey and postal prefix
                UC->>DB: begin, mint labelRef and trackingNumber
                UC->>DB: insert label, snapshot, journey LABELLED, outbox LabelIssued
                alt unique violation on trackingNumber
                    DB-->>UC: ErrTrackingNumberTaken
                    Note over UC: retry once with a fresh number, then 409 tracking-number-taken
                else unique violation on poNumber and packageNo
                    DB-->>UC: ErrLabelAlreadyIssued
                    Note over H: 409 label-already-issued, a concurrent request won
                else committed
                    UC-->>H: label metadata
                    Note over H: 201 labelRef, trackingNumber, carrier, no address
                end
            end
        end
    end
    RL->>DB: claim unpublished outbox rows
    Note over RL: publish label.LabelIssued keyed by labelRef, mark published
```

Source: `internal/application/usecases/labels.go` (`execute`, `issue`, `create`), `internal/adapters/inbound/http/idempotency.go`, `internal/adapters/outbound/customerordering/gateway.go`. The call to customer-ordering is made before the transaction opens. The label, the sealed snapshot, the journey, the outbox row and the stored idempotency answer commit together. Omitted: the ship-to validation failure branch (`validation-failed`).

## 2. VoidLabel

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http adapter
    participant UC as VoidLabel
    participant A as Label and Journey
    participant DB as DB transaction
    participant RL as Relay to Kafka
    Note over H: POST /labels/labelRef:void with an Idempotency-Key
    H->>UC: Execute labelRef, reason
    UC->>DB: load label
    alt not found
        UC-->>H: ErrNotFound
        Note over H: 404 not-found
    else already VOIDED
        UC-->>H: label unchanged
        Note over H: 200, no second event
    else ISSUED
        UC->>DB: load the journey of the same trackingNumber
        UC->>A: journey Void
        alt journey past LABELLED
            A-->>UC: ErrVoidAfterHandover
            Note over H: 409 void-after-handover
        else journey LABELLED
            UC->>A: label Void with reason
            UC->>DB: update label and journey, outbox LabelVoided
            UC-->>H: label VOIDED
            Note over H: 200
        end
    end
    RL->>DB: claim unpublished outbox rows
    Note over RL: publish label.LabelVoided keyed by labelRef
```

Source: `internal/application/usecases/labels.go` (`VoidLabel.Execute`). A journey that is already closed answers `journey-closed`. The journey closes as `VOIDED` and raises no event of its own.

## 3. RecordScan (REST)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http adapter
    participant UC as RecordScan
    participant A as Journey
    participant DB as DB transaction
    participant RL as Relay to Kafka
    Note over H: POST /journeys/trackingNumber/scans with an Idempotency-Key
    H->>UC: Execute trackingNumber, scanId, nodeId, scanType, at
    UC->>DB: load journey with its scans
    alt not found
        UC-->>H: ErrNotFound
        Note over H: 404 not-found
    else found
        UC->>A: RecordScan
        alt invalid input, closed or duplicate
            A-->>UC: validation, ErrJourneyClosed or ErrDuplicateScan
            Note over H: 422 or 409, nothing stored
        else out of order or no edge
            UC->>DB: save the scan with applied false, no event
            Note over H: 409 scan-out-of-order or illegal-transition, the scan is kept
        else applied
            UC->>DB: update journey where version matches, append scan, outbox PackageScanned and the milestone event
            alt version moved
                DB-->>UC: ErrConcurrentUpdate
                Note over UC: reload and rerun, up to 3 attempts
            else committed
                UC-->>H: journey
                Note over H: 200
            end
        end
    end
    RL->>DB: claim unpublished outbox rows
    Note over RL: publish journey events keyed by trackingNumber
```

Source: `internal/application/usecases/scans.go` (`RecordScan.Execute`), `internal/domain/journey/journey.go` (`RecordScan`), `internal/application/usecases/writer.go` (`transact`, `maxAttempts`). A third failure to commit leaves `concurrent-update` (409).

## 4. ApplyScans (Kafka consumers)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant KC as kafka consumer
    participant UC as ApplyScans
    participant A as Journey
    participant DB as DB transaction
    participant RL as Relay to Kafka
    Note over KC: VendorShipmentConfirmed, LoadDeparted, LoadArrived, PackageOutForDelivery or DeliveryAttempted
    KC->>KC: decode CloudEvent, map to one fact per trackingNumber
    KC->>UC: Execute consumer, event id, facts
    UC->>DB: claim consumer and event id in processed_events
    alt already claimed
        Note over KC: skipped, offset committed
    else claimed
        loop each fact
            UC->>DB: load journey
            alt not found
                Note over UC: counted as unknown, acknowledged
            else found
                UC->>A: RecordScan with scanId event id, colon, trackingNumber
                alt duplicate or closed
                    Note over UC: counted, nothing stored
                else out of order or no edge
                    UC->>DB: save the scan with applied false
                else applied
                    UC->>DB: save journey, outbox PackageScanned and the milestone event
                end
            end
        end
        Note over DB: claim, scans and outbox rows commit in ONE transaction
        KC->>KC: commit the offset only after success
    end
    alt error that is not a counted refusal
        Note over KC: retry with backoff, then the dead-letter topic
    end
    RL->>DB: claim unpublished outbox rows
    Note over RL: publish journey events keyed by trackingNumber
```

Source: `internal/application/usecases/scans.go` (`ApplyScans.Execute`, `applyOne`, `count`), `internal/adapters/inbound/kafka/handlers.go`, `internal/adapters/inbound/kafka/consumer.go`. The inbox claim shares the transaction with every journey of the event, so a redelivery after a commit is skipped as a whole and a rollback releases the claim. The same shape serves `PackageReturnInitiated` through `ReturnToSender` (diagram 6).

## 5. SweepSilence: RaiseException and DeclareLost

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant T as scheduler silence-sweep
    participant UC as SweepSilence
    participant A as Journey
    participant DB as DB transaction
    participant RL as Relay to Kafka
    T->>UC: Execute, at start and every SILENCE_SWEEP_INTERVAL
    loop pages of 200 candidates from ListStale
        UC->>DB: list open journeys past LABELLED with last scan older than 6 hours
        loop each candidate
            UC->>DB: begin, reload the journey
            alt unanswered NO_SCAN for 48 hours while IN_TRANSIT
                UC->>A: DeclareLost
                UC->>DB: save journey, outbox PackageLost
            else silent past the window of state and tier
                UC->>A: RaiseException
                UC->>DB: save journey, outbox PackageExceptionRaised
            else nothing to do
                Note over UC: no write
            end
            alt version moved since the reload
                DB-->>UC: ErrConcurrentUpdate
                Note over UC: counted as a conflict, seen again on the next tick
            end
        end
    end
    Note over T: logs raised, lost and conflicts
    RL->>DB: claim unpublished outbox rows
    Note over RL: publish journey events keyed by trackingNumber
```

Source: `internal/application/usecases/sweep.go`, `internal/domain/journey/types.go` (`SilenceWindow`, `MinSilenceWindow`), `internal/scheduler/scheduler.go`. Each journey is decided in its own unit, so one conflict never aborts the page.

## 6. ReturnToSender

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant KC as last-mile consumer
    participant UC as ReturnToSender
    participant A as Journey
    participant DB as DB transaction
    participant RL as Relay to Kafka
    Note over KC: PackageReturnInitiated from last-mile-delivery
    KC->>UC: Execute consumer, event id, trackingNumber, returnedAt
    UC->>DB: claim the event id
    alt already claimed
        Note over KC: skipped
    else claimed
        UC->>DB: load journey
        alt not found
            Note over KC: unknown, acknowledged
        else found
            UC->>A: ReturnToSender
            alt ErrJourneyClosed
                Note over KC: closed, acknowledged
            else not AT_STATION
                A-->>UC: ErrIllegalTransition
                Note over KC: the transaction rolls back, the consumer retries
            else AT_STATION
                UC->>DB: save journey RETURNED, outbox PackageReturned
                Note over KC: committed, offset committed
            end
        end
    end
    RL->>DB: claim unpublished outbox rows
    Note over RL: publish journey.PackageReturned keyed by trackingNumber
```

Source: `internal/application/usecases/scans.go` (`ReturnToSender.Execute`), `internal/adapters/inbound/kafka/handlers.go` (`LastMileHandlers`). A rollback also releases the inbox claim, so the retry runs again.
