---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What the Shipping & Tracking context owns and does not own, its Generic classification in the retail network, main capabilities and a map of this site."
---

# Introduction

**Shipping & Tracking** is a bounded context of the retail network (area *Network*, logistics tier), classified as a **Generic** subdomain with commodity evolution: labels and tracking timelines are bought, not built, in most networks (see [Subdomain classification](/docs/ddd/subdomain-classification)). It does two things, and both have to be right every time:

1. **It issues the label of every package.** A label carries a tracking number that is unique across the whole network, a carrier and the delivery address. The address is the one piece of personal data this context touches: it is read from customer-ordering at issue time, kept only as an AES-256-GCM encrypted snapshot, purged 30 days after the journey closes, and never logged, published or returned.
2. **It owns each package's journey.** Each building a package crosses only knows its own step: the vendor says it shipped, linehaul says a load left and arrived, the delivery station says out for delivery and then delivered or failed. This context folds those facts into one forward-only, customer-visible timeline that ends in `DELIVERED`, `RETURNED`, `LOST` or `VOIDED`, and raises an exception when a package goes silent.

Domain-driven design study: no real company, brand or dataset; names and figures are synthetic.

## What it owns

| Concept | Where |
| --- | --- |
| `ShippingLabel` aggregate (`ISSUED`, `VOIDED`), one per `(poNumber, packageNo)`, unique tracking number | `internal/domain/label` |
| `PackageJourney` aggregate with its `Scan` entities, nine states, eight scan types | `internal/domain/journey` |
| The ship-to snapshot (encrypted) and the opaque `stopKey` and postal prefix derived from the address | `internal/application/shipto`, `internal/adapters/outbound/crypto` |
| Two derived instants other contexts rely on: **handover** (the first pickup departure scan at the vendor node, `HandoverOnFirstScan`) and **delivered** | `journey.apply` |
| The silence policy: `NO_SCAN` exception per tier and state, `PackageLost` 48 h after an unanswered exception on an in-transit package | `internal/domain/journey/types.go`, `usecases/sweep.go` |
| A local read model of catalogue dimensions fed by `CatalogItemCreated` | table `catalog_dimensions` |

## What it does not own

- **The ship-to address.** customer-ordering owns it; this context only reads it once per label (`GetShipTo`).
- **Vendor handover statements, linehaul legs, last-mile attempts, catalogue items.** vendor-connectivity, linehaul-transportation, last-mile-delivery and offer-availability own them; their published events are consumed and folded into journeys.
- **Carrier integration, rate shopping, carrier choice by cost.** `carrier` is a field the label carries (`RN-EXPRESS` for `SAME_DAY` and `NEXT_DAY`, `RN-GROUND` for the others), not an integration.
- **Judging promises and scoring nodes.** delivery-promise and node-performance own those; this context supplies the facts (`PackageDelivered`, `PackageExceptionRaised`, `PackageHandedOver`).
- **Routing and loading decisions.** linehaul-transportation and last-mile-delivery decide which load or route a package rides.
- **Customer notifications and returns handling.** A future customer-notification and customer-returns context would own them; this context only ends a journey as `RETURNED`.
- **Any fleet (warehouse-systems) data.** The warehouse-systems fleet reaches the network only through the Vendor API of vendor-connectivity.

## Capabilities

| Capability | Interface | Page |
| --- | --- | --- |
| Issue a label and start the journey | REST `POST /labels` | [Use cases](/docs/ddd/use-cases) |
| Void a label before handover | REST `POST /labels/{labelRef}:void` | [Use cases](/docs/ddd/use-cases) |
| Read a package's timeline | REST `GET /journeys/{trackingNumber}`, MCP and A2A `get_journey`, the console remote | [API overview](/docs/api-reference), [MCP tools](/docs/mcp/tools) |
| Record a scan | Kafka consumers of four topics, and REST `POST /journeys/{trackingNumber}/scans` | [Event catalogue](/docs/api-reference/events) |
| Raise `NO_SCAN` exceptions and declare lost packages | periodic `silence-sweep` job | [Runbook](/docs/operations/runbook) |
| Publish eight integration events | Kafka via a transactional outbox | [Event catalogue](/docs/api-reference/events) |

## Where to go next

| I want to... | Read |
| --- | --- |
| Understand the layout and the three binaries | [Architecture](/docs/overview/architecture) |
| Run it locally | [Quickstart](/docs/overview/quickstart) |
| Configure or deploy it | [Configuration](/docs/operations/configuration), [Runbook](/docs/operations/runbook), [Before the first release](/docs/operations/before-first-release) |
| Watch or debug it | [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| Test or change it | [Testing](/docs/development/testing) |
| Understand the domain | [Ubiquitous language](/docs/ddd/ubiquitous-language), [Use cases](/docs/ddd/use-cases), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| Integrate with it | [Integration](/docs/ecosystem/integration), [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events), [MCP tools](/docs/mcp/tools) |
| See why decisions were taken | [ADR index](/docs/adr) |

The design intent also lives on the central retail-network pages at [warehouse-docs](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking); this site is as built and follows the code.
