---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
description: "UML class diagrams of the Shipping & Tracking domain (ShippingLabel, Journey, events) and of its ports, use cases and adapters, drawn from the Go packages."
---

# Class diagram

UML class diagrams for `internal/domain/**` and the application layer, drawn from the Go source. Fields are unexported in Go and shown with `-`; exported methods with `+`. Only state-changing methods and key queries are shown; read accessors are omitted.

## 1. ShippingLabel aggregate

```mermaid
classDiagram
    direction TB
    class ShippingLabel {
        <<AggregateRoot>>
        -ref Ref
        -tracking TrackingNumber
        -poNumber string
        -packageNo int
        -assignmentID string
        -orderID string
        -tier Tier
        -carrier string
        -stopKey string
        -postalPrefix string
        -status Status
        -issuedAt time.Time
        -voidReason string
        -voidedAt time.Time
        -version int64
        +Issue(in NewLabel, now)$ ShippingLabel
        +Void(reason, now) Event[]
        +Restore(s Snapshot)$ ShippingLabel
        +Snapshot() Snapshot
    }
    class Ref {
        <<ValueObject>>
        string
    }
    class TrackingNumber {
        <<ValueObject>>
        string
    }
    class Status {
        <<Enumeration>>
        ISSUED
        VOIDED
    }
    class Tier {
        <<Enumeration>>
        SAME_DAY
        NEXT_DAY
        TWO_DAY
        STANDARD
    }
    class LabelRepository {
        <<Port>>
        +Get(ctx, ref) ShippingLabel
        +FindByPackage(ctx, poNumber, packageNo) ShippingLabel
        +Save(ctx, label) error
    }
    class ShipToSnapshots {
        <<Port>>
        +Put(ctx, ref, sealed) error
        +Has(ctx, ref) bool
        +PurgeClosedBefore(ctx, cutoff) int
    }
    ShippingLabel --> Ref : identity
    ShippingLabel --> TrackingNumber
    ShippingLabel --> Status
    ShippingLabel --> Tier
    LabelRepository ..> ShippingLabel : persists
    ShipToSnapshots ..> Ref : keyed by
```

Source: `internal/domain/label/label.go`, `internal/domain/shared/shared.go`, `internal/application/ports/ports.go`. The aggregate holds no address: `ShipToSnapshots` stores the sealed form (`repository.SealedShipTo`: `key_id`, `nonce`, `ciphertext`) apart from it. The carrier is derived (`CarrierFor`). The errors `ErrLabelAlreadyIssued` and `ErrTrackingNumberTaken` are raised by the repository on its unique indexes.

## 2. Journey aggregate

```mermaid
classDiagram
    direction TB
    class Journey {
        <<AggregateRoot>>
        -tracking TrackingNumber
        -labelRef string
        -poNumber string
        -assignmentID string
        -orderID string
        -tier Tier
        -state State
        -scans Scan[]
        -startedAt time.Time
        -lastScanAt time.Time
        -lastNodeID string
        -handedOverAt time.Time
        -deliveredAt time.Time
        -closedAt time.Time
        -lastExceptedAt time.Time
        -version int64
        +Start(in NewJourney, now)$ Journey
        +RecordScan(in ScanInput, now) Result
        +RaiseException(now) Event[]
        +ReturnToSender(returnedAt, now) Event[]
        +DeclareLost(declaredAt, now) Event[]
        +Void(now) error
        +Silent(now) bool
        +Lost(now) bool
    }
    class Scan {
        <<Entity>>
        +ID string
        +NodeID string
        +Type ScanType
        +At time.Time
        +Applied bool
    }
    class ScanType {
        <<Enumeration>>
        SHIPMENT_CONFIRMED
        PICKUP_DEPARTURE
        DEPARTURE
        TRANSIT_ARRIVAL
        STATION_ARRIVAL
        OUT_FOR_DELIVERY
        DELIVERED
        DELIVERY_FAILED
    }
    class State {
        <<Enumeration>>
        LABELLED
        HANDED_OVER
        IN_TRANSIT
        AT_STATION
        OUT_FOR_DELIVERY
        DELIVERED
        RETURNED
        LOST
        VOIDED
    }
    class JourneyRepository {
        <<Port>>
        +Get(ctx, tn) Journey
        +Save(ctx, journey) error
        +ListStale(ctx, filter) Journey[]
    }
    Journey "1" *-- "0..*" Scan : scans
    Journey --> State
    Scan --> ScanType
    JourneyRepository ..> Journey : persists
```

Source: `internal/domain/journey/journey.go`, `internal/domain/journey/types.go`. The edge table `next(ScanType, State)` and the silence windows (`SilenceWindow`) are package functions, not classes. `RecordScan` reports in `Result.Changed` that the aggregate must be saved even when it returns an error (a scan stored out of order or without an edge). The errors `ErrScanOutOfOrder`, `ErrDuplicateScan`, `ErrJourneyClosed` and `ErrVoidAfterHandover` are package variables.

## 3. Domain events

Every event embeds `shared.Header` (`Version`, `At`) and implements `shared.Event` (`EventName`, `Entity`, `SubjectID`, `AggregateVersion`, `OccurredAt`). Three diagrams keep them readable.

```mermaid
classDiagram
    direction TB
    class Event {
        <<interface>>
        +EventName() string
        +Entity() string
        +SubjectID() string
        +AggregateVersion() int64
        +OccurredAt() time.Time
    }
    class LabelIssued {
        <<DomainEvent>>
        +LabelRef
        +TrackingNumber
        +PONumber
        +AssignmentID
        +OrderID
        +PackageNo
        +Tier
        +Carrier
        +StopKey
        +DestinationPostalPrefix
    }
    class LabelVoided {
        <<DomainEvent>>
        +LabelRef
        +TrackingNumber
        +Reason
    }
    Event <|.. LabelIssued
    Event <|.. LabelVoided
```

Source: `internal/domain/label/events.go`, `internal/domain/shared/shared.go`.

```mermaid
classDiagram
    direction TB
    class Event {
        <<interface>>
    }
    class PackageScanned {
        <<DomainEvent>>
        +TrackingNumber
        +ScanID
        +NodeID
        +ScanType
        +ScannedAt
        +State
    }
    class PackageHandedOver {
        <<DomainEvent>>
        +TrackingNumber
        +PONumber
        +NodeID
        +HandedOverAt
    }
    class PackageDelivered {
        <<DomainEvent>>
        +TrackingNumber
        +AssignmentID
        +OrderID
        +DeliveredAt
    }
    Event <|.. PackageScanned
    Event <|.. PackageHandedOver
    Event <|.. PackageDelivered
```

```mermaid
classDiagram
    direction TB
    class Event {
        <<interface>>
    }
    class PackageExceptionRaised {
        <<DomainEvent>>
        +TrackingNumber
        +AssignmentID
        +OrderID
        +Reason
        +RaisedAt
    }
    class PackageReturned {
        <<DomainEvent>>
        +TrackingNumber
        +OrderID
        +ReturnedAt
    }
    class PackageLost {
        <<DomainEvent>>
        +TrackingNumber
        +OrderID
        +DeclaredAt
    }
    Event <|.. PackageExceptionRaised
    Event <|.. PackageReturned
    Event <|.. PackageLost
```

Source: `internal/domain/journey/events.go`. Field names are Go names; on the wire they are camelCase (`ScannedAt` becomes `at` on `PackageScanned`, `RaisedAt` becomes `at` on `PackageExceptionRaised`), see [Domain events](/docs/ddd/domain-events). No event has an address field.

## 4. Use cases, ports and adapters

```mermaid
classDiagram
    direction LR
    class IssueLabel {
        <<UseCase>>
        +Execute(ctx, cmd) Label
    }
    class VoidLabel {
        <<UseCase>>
        +Execute(ctx, ref, reason) Label
    }
    class RecordScan {
        <<UseCase>>
        +Execute(ctx, cmd) Journey
    }
    class ApplyScans {
        <<UseCase>>
        +Execute(ctx, cmd) ApplyResult
    }
    class ReturnToSender {
        <<UseCase>>
        +Execute(ctx, cmd) ReturnResult
    }
    class SweepSilence {
        <<UseCase>>
        +Execute(ctx) SweepResult
    }
    class PurgeShipTo {
        <<UseCase>>
        +Execute(ctx) int
    }
    class RecordCatalogItem {
        <<UseCase>>
        +Execute(ctx, consumer, eventID, d) bool
    }
    class GetJourney {
        <<UseCase>>
        +Execute(ctx, tn) Journey
    }
    class ShipToGateway {
        <<Port>>
        +GetShipTo(ctx, orderID) Address
    }
    class ShipToSealer {
        <<Port>>
        +Seal(plaintext) SealedShipTo
    }
    class StopKeyer {
        <<Port>>
        +StopKey(normalized) string
    }
    class UnitOfWork {
        <<Port>>
        +Do(ctx, fn) error
    }
    class EventOutbox {
        <<Port>>
        +Append(ctx, events) error
    }
    class ProcessedEvents {
        <<Port>>
        +Claim(ctx, consumer, eventID) bool
    }
    class customerordering_Gateway {
        <<Adapter>>
    }
    class crypto_Sealer {
        <<Adapter>>
    }
    class postgres_Adapters {
        <<Adapter>>
    }
    IssueLabel ..> ShipToGateway
    IssueLabel ..> ShipToSealer
    IssueLabel ..> StopKeyer
    IssueLabel ..> UnitOfWork
    ApplyScans ..> ProcessedEvents
    ApplyScans ..> UnitOfWork
    VoidLabel ..> UnitOfWork
    RecordScan ..> UnitOfWork
    ReturnToSender ..> UnitOfWork
    SweepSilence ..> UnitOfWork
    UnitOfWork ..> EventOutbox : same transaction
    customerordering_Gateway ..|> ShipToGateway
    crypto_Sealer ..|> ShipToSealer
    crypto_Sealer ..|> StopKeyer
    postgres_Adapters ..|> UnitOfWork
    postgres_Adapters ..|> EventOutbox
    postgres_Adapters ..|> ProcessedEvents
```

Source: `internal/application/usecases/*.go`, `internal/application/ports/ports.go`, `internal/adapters/outbound/{customerordering,crypto,postgres}`. Omitted: the repositories, `Clock`, `IDGenerator`, `EventEncoder` and `LabelMetrics` ports, and `Deps`, the struct that hands them to every use case. `PurgeShipTo`, `RecordCatalogItem` and `GetJourney` use the repositories only.

The package layout and the dependency rules enforced by `internal/architecture` are on [Architecture](/docs/overview/architecture).
