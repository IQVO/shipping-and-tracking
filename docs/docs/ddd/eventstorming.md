---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
description: "Process-level EventStorming boards for Shipping & Tracking: label issue and void, timeline consolidation, and exceptions and terminal outcomes, with the hotspots that remain."
---

# EventStorming

Process-level boards in the ddd-crew [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) notation. Every sticky comes from the code of this repository; the hotspots are the gaps or risks that remain in what is built, and the inventory at the end says where each one comes from. The domain-wide board is [EventStorming big picture](https://iqvo.github.io/warehouse-docs/network/strategic-design/eventstorming-big-picture).

## Legend

| Colour | Sticky | Meaning |
| --- | --- | --- |
| Blue `#a5d8ff` | Command | An intent this context handles |
| Yellow `#ffec99` | Aggregate | `ShippingLabel` or `PackageJourney` |
| Orange `#ffc078` | Domain event | A fact, this context's or a neighbour's |
| Lilac `#d0bfff` | Policy | "Whenever X, then Y" |
| Green `#b2f2bb` | Read model | Data a decision reads |
| Pink `#fcc2d7` | External system | A neighbour context or actor |
| Red `#ff8787` | Hotspot | A gap or risk |

## 1. Label issue

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 30}}}%%
flowchart TB
    VC["vendor-connectivity<br/>vendor asked for a label"]:::external
    C1["IssueLabel<br/>POST /labels"]:::command
    CO["customer-ordering<br/>GetShipTo"]:::external
    G1["ShippingLabel"]:::aggregate
    E1["LabelIssued"]:::event
    C2["Start journey"]:::command
    G2["PackageJourney<br/>LABELLED"]:::aggregate
    LH["linehaul-transportation<br/>last-mile-delivery"]:::external
    CO2["customer-ordering<br/>order tracking"]:::external
    H2["Hotspot: printable label<br/>not rendered by any route"]:::hotspot
    VC --> C1
    C1 --> CO
    C1 --> G1
    G1 --> E1
    C1 --> C2
    C2 --> G2
    E1 --> LH
    E1 --> CO2
    G1 --- H2
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#f08c00,color:#000
    classDef event fill:#ffc078,stroke:#e8590c,color:#000
    classDef policy fill:#d0bfff,stroke:#7048e8,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/labels.go` (`IssueLabel.execute`, `create`), `internal/domain/label/label.go`, `internal/domain/journey/journey.go` (`Start`). The label, its sealed snapshot, the journey and the outbox row are one transaction. Omitted: the duplicate-label and ship-to-unavailable branches (see [Use cases](/docs/ddd/use-cases)) and the outbox relay.

### 1b. Label void

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 30}}}%%
flowchart TB
    U["caller not named"]:::external
    C3["VoidLabel<br/>POST /labels/labelRef:void"]:::command
    G1["ShippingLabel<br/>ISSUED to VOIDED"]:::aggregate
    G2["PackageJourney<br/>LABELLED to VOIDED"]:::aggregate
    E2["LabelVoided"]:::event
    X["customer-ordering,<br/>linehaul-transportation"]:::external
    H4["Hotspot: void caller unnamed"]:::hotspot
    U --> C3
    C3 --> G1
    C3 --> G2
    G1 --> E2
    E2 --> X
    C3 --- H4
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#f08c00,color:#000
    classDef event fill:#ffc078,stroke:#e8590c,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/labels.go` (`VoidLabel.Execute`), `internal/domain/journey/journey.go` (`Void`). The journey closes as `VOIDED` in the same transaction and raises no event of its own; a handed-over package is `ErrVoidAfterHandover`.

## 2. Timeline consolidation

### 2a. Handover and the middle mile

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 30}}}%%
flowchart TB
    VC["vendor-connectivity"]:::external
    LH["linehaul-transportation"]:::external
    F1["VendorShipmentConfirmed"]:::event
    F2["LoadDeparted"]:::event
    F3["LoadArrived"]:::event
    P0["Policy: each fact becomes<br/>one scan per tracking number"]:::policy
    C1["RecordScan"]:::command
    G1["PackageJourney"]:::aggregate
    P1["Policy: handover on first<br/>pickup departure"]:::policy
    E1["PackageScanned"]:::event
    E2["PackageHandedOver"]:::event
    NP["node-performance"]:::external
    CO["customer-ordering"]:::external
    H1["Risk: a load event for a<br/>package that has no journey<br/>is counted and acknowledged"]:::hotspot
    VC --> F1
    LH --> F2
    LH --> F3
    F1 --> P0
    F2 --> P0
    F3 --> P0
    P0 --> C1
    C1 --> G1
    P1 --> G1
    G1 --> E1
    G1 --> E2
    E1 --> CO
    E2 --> NP
    F3 --- H1
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#f08c00,color:#000
    classDef event fill:#ffc078,stroke:#e8590c,color:#000
    classDef policy fill:#d0bfff,stroke:#7048e8,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/adapters/inbound/kafka/handlers.go` (`VendorHandlers`, `LinehaulHandlers`), `internal/application/usecases/scans.go` (`ApplyScans`), `internal/domain/journey/journey.go` (`apply`). Omitted: the REST route `POST /journeys/{trackingNumber}/scans` (same use case, caller not named); duplicate and out-of-order scans (stored or refused, no event).

### 2b. Last mile

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 30}}}%%
flowchart TB
    LM["last-mile-delivery"]:::external
    F4["PackageOutForDelivery"]:::event
    F5["DeliveryAttempted"]:::event
    P0["Policy: each fact becomes<br/>one scan per tracking number"]:::policy
    C1["RecordScan"]:::command
    G1["PackageJourney"]:::aggregate
    E1["PackageScanned"]:::event
    E3["PackageDelivered"]:::event
    DS["delivery-promise,<br/>order-sourcing"]:::external
    CO["customer-ordering"]:::external
    LM --> F4
    LM --> F5
    F4 --> P0
    F5 --> P0
    P0 --> C1
    C1 --> G1
    G1 --> E1
    G1 --> E3
    E1 --> CO
    E3 --> DS
    E3 --> CO
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#f08c00,color:#000
    classDef event fill:#ffc078,stroke:#e8590c,color:#000
    classDef policy fill:#d0bfff,stroke:#7048e8,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Source: `internal/adapters/inbound/kafka/handlers.go` (`LastMileHandlers`). A failed attempt returns the journey to `AT_STATION` and raises `PackageScanned` only.

## 3. Exceptions and terminal outcomes

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 30}}}%%
flowchart TB
    T["silence-sweep timer"]:::external
    P1["Policy: exception on silence<br/>no applied scan in the window"]:::policy
    C1["RaiseException"]:::command
    G1["PackageJourney"]:::aggregate
    E1["PackageExceptionRaised<br/>NO_SCAN"]:::event
    DP["delivery-promise"]:::external
    CO["customer-ordering"]:::external
    LM["last-mile-delivery<br/>PackageReturnInitiated"]:::external
    C2["ReturnToSender"]:::command
    P2["Policy: lost after 48 hours<br/>unanswered while IN_TRANSIT"]:::policy
    C3["DeclareLost"]:::command
    E2["PackageReturned"]:::event
    E3["PackageLost"]:::event
    H1["Risk: a sweep tick can lose<br/>a race to a late scan,<br/>counted as a conflict"]:::hotspot
    T --> P1
    P1 --> C1
    C1 --> G1
    G1 --> E1
    E1 --> DP
    E1 --> CO
    LM --> C2
    C2 --> G1
    T --> P2
    P2 --> C3
    C3 --> G1
    G1 --> E2
    G1 --> E3
    P1 --- H1
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#f08c00,color:#000
    classDef event fill:#ffc078,stroke:#e8590c,color:#000
    classDef policy fill:#d0bfff,stroke:#7048e8,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/sweep.go` (`SweepSilence`, `decide`), `internal/application/usecases/scans.go` (`ReturnToSender`), `internal/domain/journey/types.go`. Omitted: `ErrJourneyClosed` on a closed journey.

## Sticky inventory

| Sticky | Kind | Source |
| --- | --- | --- |
| `IssueLabel`, `VoidLabel` | Command | `usecases/labels.go` |
| Start journey, `RecordScan`, `RaiseException`, `ReturnToSender`, `DeclareLost` | Command | `journey/journey.go`, `usecases/scans.go`, `usecases/sweep.go` |
| `ShippingLabel`, `PackageJourney` | Aggregate | `domain/label`, `domain/journey` |
| `LabelIssued`, `LabelVoided`, `PackageScanned`, `PackageHandedOver`, `PackageDelivered`, `PackageExceptionRaised`, `PackageReturned`, `PackageLost` | Domain event (own) | `label/events.go`, `journey/events.go` |
| `VendorShipmentConfirmed`, `LoadDeparted`, `LoadArrived`, `PackageOutForDelivery`, `DeliveryAttempted`, `PackageReturnInitiated` | Domain event (neighbour) | `inbound/kafka/handlers.go` |
| Handover on first pickup departure, exception on silence, lost after 48 hours | Policy | `journey.apply`, `journey.SilenceWindow`, `Journey.Lost` |
| `catalog_dimensions` | Read model | `ports.CatalogDimensionsView` |
| `vendor-connectivity`, `customer-ordering`, `linehaul-transportation`, `last-mile-delivery`, `delivery-promise`, `order-sourcing`, `node-performance` | External system | `apis/asyncapi.yaml`, `outbound/customerordering` |

## Hotspots and where they come from

| Hotspot | Board | Source |
| --- | --- | --- |
| Printable label not rendered | 1 | No route of `apis/openapi.yaml` returns anything printable; the response is the label metadata |
| `VoidLabel` and `RecordScan` have no named caller | 1b, 2a | The routes are unauthenticated and the contracts name no caller |
| A load or route event for an unknown tracking number | 2a | `ApplyScans` counts it as unknown and acknowledges it (`internal/application/usecases/scans.go`) |
| Sweep race with a late scan | 3 | `SweepSilence` counts `ErrConcurrentUpdate` as a conflict and sees the journey again on the next tick |
