---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
description: "Key Shipping & Tracking scenarios as ddd-crew domain message flows: label issue, handover to delivery, the failed-attempt, return and silence paths."
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling). Participants are contexts and actors. Every arrow is a message this repository sends or handles, prefixed `cmd:`, `qry:` or `evt:`. Events are written `<entity>.<EventName>`; the full type is `com.retailnetwork.<tier>.<producer>.<entity>.<EventName>` (see [Domain events](/docs/ddd/domain-events)). Kafka is not drawn: an event arrow goes straight from producer to consumer. The domain-wide flows are on the central [domain message flows](https://iqvo.github.io/warehouse-docs/network/strategic-design/domain-message-flows) page; the same scenarios as adapter-to-Kafka sequences are on [Sequence diagrams](/docs/ddd/sequence-diagrams).

## 1. A label is issued

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant VC as vendor-<br/>connectivity
    participant ST as shipping-<br/>and-tracking
    participant CO as customer-<br/>ordering
    participant LH as linehaul-<br/>transportation
    participant LM as last-mile-<br/>delivery
    VC->>ST: cmd: IssueLabel<br/>POST /labels
    ST->>CO: qry: GetShipTo<br/>GET /orders/orderId/ship-to
    CO-->>ST: qry: reply ship-to
    Note over ST: label ISSUED, sealed<br/>snapshot, journey LABELLED
    ST-->>VC: cmd: reply labelRef,<br/>trackingNumber, carrier
    ST->>LH: evt: label.LabelIssued
    ST->>LM: evt: label.LabelIssued
    ST->>CO: evt: label.LabelIssued
```

Source: `internal/application/usecases/labels.go` (`IssueLabel`), `internal/adapters/outbound/customerordering/gateway.go`, `apis/asyncapi.yaml` (consumers of `LabelIssued`). The request carries `poNumber`, `packageNo`, `assignmentId`, `orderId` and `tier`. Omitted: how vendor-connectivity answers the vendor (never with the address) and the failure branches (ship-to unavailable, duplicate package), drawn on [Sequence diagrams](/docs/ddd/sequence-diagrams).

## 2. Handover and the middle mile

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant VC as vendor-<br/>connectivity
    participant LH as linehaul-<br/>transportation
    participant ST as shipping-<br/>and-tracking
    participant NP as node-<br/>performance
    participant CO as customer-<br/>ordering
    VC->>ST: evt: purchaseorder.<br/>VendorShipmentConfirmed
    Note over ST: SHIPMENT_CONFIRMED scan,<br/>stays LABELLED
    ST->>CO: evt: journey.PackageScanned<br/>LABELLED
    LH->>ST: evt: load.LoadDeparted<br/>kind PICKUP at the vendor node
    Note over ST: first pickup departure<br/>of a LABELLED journey = handover
    ST->>NP: evt: journey.<br/>PackageHandedOver
    ST->>CO: evt: journey.PackageScanned<br/>HANDED_OVER
    LH->>ST: evt: load.LoadArrived<br/>at a sortation centre
    ST->>CO: evt: journey.PackageScanned<br/>IN_TRANSIT
    LH->>ST: evt: load.LoadArrived<br/>at the delivery station
    ST->>CO: evt: journey.PackageScanned<br/>AT_STATION
```

Source: `internal/adapters/inbound/kafka/handlers.go` (`VendorHandlers`, `LinehaulHandlers`), `internal/domain/journey/journey.go` (`apply`). A delivery station is told from a sortation centre by the `nodeType` that `LoadArrived` carries, and a pickup from a linehaul departure by its `departureKind`. A direct-injection lane from the vendor node to the station arrives through the edge `HANDED_OVER` to `AT_STATION`. Omitted: the other consumers of the same upstream events.

## 3. Out for delivery and delivered

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant LM as last-mile-<br/>delivery
    participant ST as shipping-<br/>and-tracking
    participant DP as delivery-<br/>promise
    participant OS as order-<br/>sourcing
    participant CO as customer-<br/>ordering
    LM->>ST: evt: route.<br/>PackageOutForDelivery
    ST->>CO: evt: journey.PackageScanned<br/>OUT_FOR_DELIVERY
    LM->>ST: evt: route.DeliveryAttempted<br/>outcome DELIVERED
    Note over ST: journey DELIVERED, terminal
    ST->>CO: evt: journey.PackageScanned<br/>DELIVERED
    ST->>DP: evt: journey.<br/>PackageDelivered
    ST->>OS: evt: journey.<br/>PackageDelivered
    ST->>CO: evt: journey.<br/>PackageDelivered
```

Source: `internal/adapters/inbound/kafka/handlers.go` (`LastMileHandlers`), `internal/domain/journey/journey.go` (`apply`). `PackageScanned` and `PackageDelivered` are raised in one transaction. What delivery-promise and order-sourcing do with `PackageDelivered` is theirs to document.

## 4. Failed attempt, return and silence

The most important failure paths: a package that stops moving must reach delivery-promise before the promise date passes, and a package that cannot be delivered must end as `RETURNED`.

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant LH as linehaul-<br/>transportation
    participant LM as last-mile-<br/>delivery
    participant ST as shipping-<br/>and-tracking
    participant DP as delivery-<br/>promise
    participant CO as customer-<br/>ordering
    LM->>ST: evt: route.DeliveryAttempted<br/>outcome FAILED
    Note over ST: DELIVERY_FAILED scan,<br/>back to AT_STATION
    ST->>CO: evt: journey.PackageScanned<br/>AT_STATION
    opt a late scan arrives
        LH->>ST: evt: load.LoadArrived<br/>older arrivedAt
        Note over ST: ErrScanOutOfOrder<br/>stored, not applied
    end
    alt third failed attempt
        LM->>ST: evt: packageattempthistory.<br/>PackageReturnInitiated
        Note over ST: AT_STATION to RETURNED
        ST->>DP: evt: journey.<br/>PackageReturned
    else silence
        Note over ST: no applied scan within<br/>the window of state and tier
        ST->>ST: cmd: RaiseException<br/>NO_SCAN
        ST->>DP: evt: journey.<br/>PackageExceptionRaised
        ST->>CO: evt: journey.<br/>PackageExceptionRaised
        Note over ST: 48 hours unanswered<br/>while IN_TRANSIT
        ST->>ST: cmd: DeclareLost
        ST->>DP: evt: journey.<br/>PackageLost
    end
```

Source: `internal/application/usecases/scans.go` (`ReturnToSender`), `internal/application/usecases/sweep.go` (`SweepSilence`), `internal/domain/journey/types.go` (`SilenceWindow`, `LostAfterExceptionHours`). A `PackageReturnInitiated` that arrives before the journey is `AT_STATION` is retried because the failed-attempt scan may still be on its way. Omitted: the next wave's `PackageOutForDelivery`, and what delivery-promise does with the exception.
