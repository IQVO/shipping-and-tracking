---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "Why Shipping & Tracking is a Generic subdomain, with the classification of its neighbours in the retail network."
---

# Subdomain classification

**Verdict: Generic**, CloudEvents tier `logistics`, business model compliance and experience, evolution **commodity**. This is the retail-network model's verdict for the context, restated in [ADR 0001](/docs/adr/0001-shipping-and-tracking-bounded-context). The neighbour table below is copied from the central [subdomain classification](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification) page of warehouse-docs.

## Why Generic

The test for Core is not "is it hard" but "would buying it, and conforming to the vendor's model, cost us the reason customers choose us". Here it would not:

- **Low differentiation.** The context makes no commercial decision. It never chooses a node, a path, a load or a route. It issues an identifier, keeps an address safe and records what other contexts report. Every retail and parcel business has labels and tracking; doing them well is expected, doing them differently wins no customer. The network differentiates on the delivery promise and on order sourcing, which are Core and live elsewhere.
- **Real but undifferentiating complexity.** The timeline is the hard part: `PackageJourney` has nine states and a single edge table (`internal/domain/journey/types.go`), is fed by facts from three producers on three topics that arrive in no guaranteed order, with one load event naming many tracking numbers. It must apply scans in time order, refuse duplicates, store late scans unapplied, and close for good (`ErrScanOutOfOrder`, `ErrDuplicateScan`, `ErrJourneyClosed`). The label side adds a synchronous read of personal data and encryption at rest. This is real engineering, not a differentiator.
- **The two policies carry the little business weight.** `HandoverOnFirstScan` defines the handover instant that node-performance scores vendors against, and `ExceptionOnSilence` lets delivery-promise re-promise before the customer notices. They are small policies on top of a generic timeline.

| Part | Evolution stage | Why |
| --- | --- | --- |
| `ShippingLabel` issue | Commodity | One label per package, a unique number, an address. The only network-specific rule is confining the personal data |
| `PackageJourney` timeline | Product, tending to commodity | The consolidation rules are specific to this network's event names, the shape (forward-only scans, terminal outcomes) is standard |
| Handover and silence policies | Custom-built, small | Network choices on top of a generic timeline |

## Movement to watch

A Generic context is a candidate to buy rather than build. If the network adopted an external tracking or label product, this context would shrink to an anti-corruption layer that keeps the event names other contexts consume (`LabelIssued`, `PackageHandedOver`, `PackageDelivered`, `PackageExceptionRaised`). The two policies would stay with the network because node-performance and delivery-promise depend on their exact definitions. Nothing in the code or the ADRs records such a decision; it would be a new ADR.

## Neighbours

Copied from warehouse-docs `network/docs/strategic-design/subdomain-classification.md` on `origin/main`:

| Bounded context | Classification | Tier | Business model | Evolution | Relationship to this context |
| --- | --- | --- | --- | --- | --- |
| `delivery-promise` | Core | `commerce` | Revenue (conversion) and cost | Custom-built | Downstream: consumes `PackageDelivered`, `PackageExceptionRaised` |
| `order-sourcing` | Core | `commerce` | Cost-to-serve reduction | Custom-built | Downstream: consumes `PackageDelivered` |
| `customer-ordering` | Generic | `commerce` | Engagement | Commodity | Upstream (`GetShipTo`) and downstream (events, `GetJourney`) |
| `vendor-connectivity` | Supporting | `partner` | Revenue-channel enabler | Product | Calls `POST /labels`; publishes `VendorShipmentConfirmed` |
| `linehaul-transportation` | Supporting | `logistics` | Cost reducer, speed enabler | Custom-built | Upstream (`LoadDeparted`, `LoadArrived`); downstream of `LabelIssued` |
| `last-mile-delivery` | Supporting | `logistics` | Cost reducer, experience creator | Custom-built | Upstream (`PackageOutForDelivery`, `DeliveryAttempted`, `PackageReturnInitiated`) |
| `offer-availability` | Supporting | `commerce` | Revenue enabler | Product | Upstream (`CatalogItemCreated`) |
| `node-performance` | Supporting | `partner` | Risk reducer | Custom-built | Downstream: consumes `PackageHandedOver` |
| `shipping-and-tracking` (this context) | Generic | `logistics` | Compliance and experience | Commodity | |

The central page counts two Core, seven Supporting and two Generic contexts in the network, and records that the problem-space subdomain "Shipping Labels & Tracking" is Generic.
