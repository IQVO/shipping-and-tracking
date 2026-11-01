---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: "The terms Shipping & Tracking speaks, the Go identifiers that carry them, and the neighbour terms they must not be confused with."
---

# Ubiquitous language

Every term this context speaks, mapped to the identifier the code uses. The first table is the vocabulary of the two aggregates and their value objects, the second is the vocabulary of the silence policy and the closing commands, the third holds terms owned by neighbouring contexts that these pages use. The network-wide glossary is on the central [Ubiquitous Language](https://iqvo.github.io/warehouse-docs/network/strategic-design/ubiquitous-language) page of warehouse-docs.

## Terms of this context

Source: `internal/domain/label/label.go`, `internal/domain/journey/journey.go`, `internal/domain/journey/types.go`, `internal/domain/shared/shared.go`.

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Tracking Number** | The network-wide unique id of one package; `RN` plus 16 base-32 characters when minted here. Root id of the journey, unique column of the label | `shared.TrackingNumber` (alias `journey.TrackingNumber`) | `labelRef` (the label's id) and `poNumber` (the vendor instruction, which may cover several packages) |
| **Shipping Label** | What is printed on the package: tracking number, tier, carrier and two values derived from the address. The address itself is not a field | `label.ShippingLabel` | The Vendor API **Label** of vendor-connectivity, which is only `labelRef`, `trackingNumber` and `carrier` |
| **Label Ref** | The identity of one `ShippingLabel`, minted as `lbl-<uuid>` | `label.Ref` | `trackingNumber` |
| **Package No** | The number of a package within one purchase order, 1 to 2147483647; `(poNumber, packageNo)` has at most one label | `label.ShippingLabel.PackageNo`, `label.MaxPackageNo` | `lineNo` of a purchase-order line |
| **Tier** | The service tier the package was sold under: `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD` | `shared.Tier` | The carrier |
| **Carrier** | Who carries the package as printed on the label: `RN-EXPRESS` for `SAME_DAY` and `NEXT_DAY`, `RN-GROUND` otherwise | `label.CarrierFor`, `label.CarrierExpress`, `label.CarrierGround` | A last-mile delivery service partner |
| **Label status** | `ISSUED` or `VOIDED` | `label.Status` | Journey state |
| **Void** | Cancel an issued label while the package is still `LABELLED` (use case `VoidLabel`, event `LabelVoided`) | `label.ShippingLabel.Void`, `journey.Journey.Void` | Purchase-order cancellation in vendor-connectivity |
| **Ship-to snapshot** | The AES-256-GCM sealed copy of the address taken from customer-ordering at issue time, kept only to print the label. Never published, never returned, deleted 30 days after the journey closes | `repository.SealedShipTo`, table `ship_to_snapshots`, `usecases.ShipToRetention` | The **Ship-To** that customer-ordering owns, the source of truth |
| **Ship-to address** | The in-memory address value object. It redacts itself in logs (`[ship-to redacted]`) and exists only inside one `IssueLabel` call | `shipto.Address` | The snapshot, which is its encrypted form |
| **Stop key** | The opaque HMAC-SHA-256 of the normalised address, 64 lower-case hex characters, so last-mile can group stops without seeing an address | `label.StopKeyLen`, `ports.StopKeyer`, field `stopKey` | A postal code |
| **Destination postal prefix** | The first five letters or digits of the postal code (`shipto.PrefixLen`) | `label.ShippingLabel.DestinationPostalPrefix` | The full postal code, which is never kept |
| **Package Journey** | The customer-visible, forward-only timeline of one package, identified by its tracking number; one per label | `journey.Journey` (the `PackageJourney` aggregate), table `package_journeys` | customer-ordering's order tracking view, a read model per order fed by this context's events |
| **Journey state** | `LABELLED`, `HANDED_OVER`, `IN_TRANSIT`, `AT_STATION`, `OUT_FOR_DELIVERY` (open); `DELIVERED`, `RETURNED`, `LOST`, `VOIDED` (terminal) | `journey.State`, `State.Terminal` | Customer order status in customer-ordering |
| **Scan** | One observation of a package at a node, stored append-only | `journey.Scan`, table `journey_scans` | A last-mile delivery attempt, which this context turns into a scan |
| **Scan type** | The kind of observation: `SHIPMENT_CONFIRMED`, `PICKUP_DEPARTURE`, `DEPARTURE`, `TRANSIT_ARRIVAL`, `STATION_ARRIVAL`, `OUT_FOR_DELIVERY`, `DELIVERED`, `DELIVERY_FAILED` | `journey.ScanType`, `journey.AllScanTypes` | The departure kind in linehaul (`PICKUP`, `LINEHAUL`, `AIR`) |
| **Applied scan** | A scan that moved the journey. An out-of-order scan or one with no edge is stored with `applied=false` | `journey.Scan.Applied` | A duplicate scan, which is refused and not stored |
| **Handover** | The instant a vendor node's package enters the network: the first `PICKUP_DEPARTURE` scan, `LABELLED` to `HANDED_OVER` (`HandoverOnFirstScan`) | `Journey.HandedOverAt`, event `PackageHandedOver` | `VendorShipmentConfirmed`, the vendor's own statement that it shipped |
| **Delivered** | The instant of the `DELIVERED` scan, which also closes the journey | `Journey.DeliveredAt`, event `PackageDelivered` | A delivery attempt that failed |

## Silence policy and closing commands

Source: `internal/domain/journey/types.go`, `internal/domain/journey/journey.go`, `internal/application/usecases/sweep.go`.

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Silence** | No applied scan within the window of the current state and tier (6, 24, 48 or 72 hours; 12 hours while out for delivery). `LABELLED` and terminal states are not watched (`ExceptionOnSilence`) | `journey.SilenceWindow`, `Journey.Silent` | Node lateness scored by node-performance |
| **Package exception** | A raised problem with a package; the only reason is `NO_SCAN`. The state does not change and one exception is raised per silence | `Journey.RaiseException`, `journey.ReasonNoScan`, event `PackageExceptionRaised` | A failed delivery attempt, which only returns the journey to `AT_STATION` |
| **Unanswered exception** | A `NO_SCAN` exception with no applied scan since | `Journey.unanswered` (unexported) | A closed journey |
| **Return to sender** | End the journey as `RETURNED` from `AT_STATION` (`ReturnToSender`) | `Journey.ReturnToSender`, event `PackageReturned` | A customer return, which this context does not model |
| **Lost** | End the journey as `LOST` from `IN_TRANSIT` after a `NO_SCAN` exception stayed unanswered for 48 hours (`DeclareLost`) | `Journey.DeclareLost`, `journey.LostAfterExceptionHours`, event `PackageLost` | An exception, which keeps the journey open |
| **Fact** | One observation extracted from a consumed event, one per tracking number | `usecases.Fact`, `usecases.ApplyScans` | A scan, which is the stored form |
| **Inbox claim** | The `(consumer, event_id)` row that makes a consumer idempotent | `ports.ProcessedEvents`, table `processed_events` | The idempotency key of a REST call |
| **Idempotency key** | The `Idempotency-Key` header of the three POST routes; the stored answer is replayed for 24 hours | table `idempotency_keys`, `internal/adapters/inbound/http/idempotency.go` | The CloudEvents `id` |
| **Catalog dimensions** | Local, event-fed copy of a catalogue item's title, dimensions, weight and handling flags | `repository.CatalogDimensions`, `ports.CatalogDimensionsView`, table `catalog_dimensions` | The `CatalogItem` aggregate in offer-availability |

## Neighbour terms used here

These terms are owned by other contexts; this table only says which one and what it is not.

| Term | Owner | Meaning there | Not to be confused with |
| --- | --- | --- | --- |
| **Purchase Order (PO)** | vendor-connectivity | The instruction to one vendor node to ship one shipment group, carrying `poNumber` | The customer order |
| **Ship-To** | customer-ordering | The delivery address, the only source of truth | The ship-to snapshot kept here |
| **Customer Order** | customer-ordering | What the shopper bought, with a promise per shipment group; `orderId` | A package (one order can have many) |
| **Fulfillment Assignment** | order-sourcing | The decision of which node and path fulfil one shipment group; `assignmentId` | A purchase order |
| **Load** | linehaul-transportation | The packages consolidated onto one departure; one `LoadDeparted` names many tracking numbers | A scan (one load event becomes one scan per tracking number) |
| **Pickup Departure** | linehaul-transportation | The departure that collects packages at a vendor node (`departureKind` `PICKUP`) | Handover, which is the scan of that departure |
| **Delivery Attempt** | last-mile-delivery | One try to hand a package over, `DELIVERED` or not | The `DELIVERED` journey state, entered only on a successful attempt |
| **Catalog Item** | offer-availability | The network's own entry for a product | A package |
| **Promise Kept / Broken** | delivery-promise | The judgement at delivery against the promised date | `PackageDelivered`, the fact the judgement uses |
| **On-Time Handover Rate** | node-performance | Share of packages handed over before the required ship-by | Handover itself, which this context only reports |

This context does not touch the warehouse-systems fleet, so there is no fleet translation table.
