---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts index
sidebar_position: 10
description: "Index of the ddd-crew artifacts of Shipping & Tracking: canvases, charts, maps, flows, UML and ER diagrams, each derived from the code."
---

# DDD artifacts (ddd-crew)

The strategic and tactical design of `shipping-and-tracking`, drawn with the [ddd-crew](https://github.com/ddd-crew) tools plus UML and ER diagrams. Every diagram is Mermaid, checked against the code and ADRs on `develop`, and carries a `Source:` line naming the files it came from.

| Artifact | ddd-crew tool or notation | What it shows |
| --- | --- | --- |
| [Core domain chart](/docs/ddd/core-domain-chart) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) | Where this context sits on business differentiation against model complexity, and why it is Generic |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas) | Purpose, classification, roles, every inbound and outbound message, business decisions |
| [Context map](/docs/ddd/context-map) | [Context Mapping](https://github.com/ddd-crew/context-mapping) | This context's slice of the network map: pattern per edge, channel and messages |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) | `ShippingLabel` and `PackageJourney`: states, invariants, commands, events, concurrency |
| [Domain message flow](/docs/ddd/domain-message-flow) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) | The key scenarios as numbered command, event and query flows across contexts |
| [EventStorming](/docs/ddd/eventstorming) | [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) | Process-level boards with the sticky colours, plus hotspots |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Glossary | Every term with the Go identifier that implements it |
| [Class diagram](/docs/ddd/class-diagram) | UML class diagram | Domain types, events, use cases, ports and the adapters that implement them |
| [Entity relationship](/docs/ddd/entity-relationship) | ER diagram | The Postgres schema created by `0001_schema.up.sql`: eight tables, keys and indexes |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | UML sequence diagram | Issue label, void, record scan, apply scans, silence sweep and return, with error branches |
| [Domain events](/docs/ddd/domain-events) | Event catalogue | Every event published and consumed: full CloudEvents type, topic, key, payload, producer, consumers |

Related pages outside the pack: [Use cases](/docs/ddd/use-cases), [Subdomain classification](/docs/ddd/subdomain-classification), [Integration](/docs/ecosystem/integration) and the [event catalogue](/docs/api-reference/events).

:::note Sources of truth
The code wins. Domain rules come from `internal/domain/label`, `internal/domain/journey` and `internal/domain/shared`, behaviour from `internal/application/usecases`, the envelope from `internal/adapters/kafka/cloudevents`, the schema from `internal/adapters/outbound/postgres/migrations`, the REST contract from `apis/openapi.yaml`, the event contract from `apis/asyncapi.yaml`, and decisions from the ADRs in `docs/adr/`. Where a page and the code disagree, the page is wrong.
:::

:::info Not in this pack
The MCP server ([ADR 0006](/docs/adr/0006-mcp-server-adoption)), the A2A agent ([ADR 0009](/docs/adr/0009-a2a-inbound-adapter)) and the console remote `web/` ([ADR 0007](/docs/adr/0007-console-remote)) are built, but they are read surfaces over the model and add no aggregate, entity or event of their own, so the pack has no pages for them. The application layer, the HTTP and Kafka adapters, the outbox relay and the database schema are covered by the pages above.
:::
