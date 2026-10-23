# 1. Shipping & Tracking is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: retail-network-docs `model/model.yaml`, context `shipping-and-tracking`, and its ADRs RN-0001 and RN-0007.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. Issues shipping labels (the only place outside customer-ordering that ever sees a ship-to address) and owns each package's journey: one customer-visible timeline consolidated from vendor handover, load and delivery facts, ending in delivered, returned or lost.

## Decision

- This repo owns the aggregates ShippingLabel, PackageJourney; the invariants and error names are those of the model.
- Business decisions of the context:
- A tracking number is unique network-wide and is issued once per package.
- The address printed on the label is fetched from customer-ordering at issue time and never published.
- The journey only moves forward; a duplicate or out-of-order scan is recorded, not applied.
- A package not scanned within its expected window is raised as an exception.
- Events use the network CloudEvents namespace `com.retailnetwork.logistics.shipping-and-tracking.<entity>.<Event>` on `retailnetwork.shipping-and-tracking.events`
  (analytics-only events on `.analytics`). Published: LabelIssued, LabelVoided, PackageScanned, PackageHandedOver, PackageDelivered, PackageExceptionRaised, PackageReturned, PackageLost. No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
