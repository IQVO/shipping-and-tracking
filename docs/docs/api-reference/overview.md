---
id: overview
title: API overview
sidebar_label: API overview
slug: /api-reference
sidebar_position: 1
description: "Interfaces of Shipping & Tracking: REST, Kafka events, MCP and A2A, the endpoint table and the contract conventions."
---

# API overview

:::info Implemented by cmd/api, cmd/mcp and cmd/a2a
`cmd/api` serves every REST route below (`internal/adapters/inbound/http`) on `HTTP_ADDR` (default `:8080`). The contracts are `apis/openapi.yaml` and `apis/asyncapi.yaml`, pinned by [ADR 0001](/docs/adr/0001-shipping-and-tracking-bounded-context), [ADR 0002](/docs/adr/0002-aggregates-and-invariants) and [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). The router registers exactly the four API routes plus `/healthz` and `/readyz`; the two probe routes are not in `openapi.yaml`.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 0.1.0) | The pages under *REST* in the sidebar are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Server named in the spec: `http://localhost:8080`. Through Kong (port 8000) the API is at `/api/shipping-and-tracking` with the prefix stripped (`helm-values/shipping-and-tracking.yaml` in warehouse-infra). |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Eight published events on `retailnetwork.shipping-and-tracking.events`, seven consumed types from four topics. Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0006](/docs/adr/0006-mcp-server-adoption), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` and `/` (port 8090; through Kong `/api/mcp/shipping-and-tracking`). One read-only tool, `get_journey` (`trackingNumber`), no write tool. No auth, by fleet decision. See [MCP tools](/docs/mcp/tools). |
| A2A | [ADR 0009](/docs/adr/0009-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/shipping-and-tracking`), the Agent Card at `/.well-known/agent-card.json`. One read-only skill, `get_journey` (`trackingNumber`), no write skill. The request is a message whose first part carries `{"skill":"get_journey","arguments":{"trackingNumber":"..."}}`; unknown fields are refused. Domain rejections (`not-found`, `validation-failed`, `internal-error`) are answered inside the reply message as RFC 9457-shaped problems, never as JSON-RPC errors. Message-only agent, no streaming. No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Labels | `POST /labels` | `issueLabel` | `201`, the label metadata (`labelRef`, `trackingNumber`, `carrier`, ...), never the address. Reads the ship-to from customer-ordering first |
| Labels | `POST /labels/{labelRef}:void` | `voidLabel` | `200`, the label (`VOIDED`); a label already voided is returned unchanged |
| Journeys | `GET /journeys/{trackingNumber}` | `getJourney` | `200`, the journey with every stored scan; `404 not-found` |
| Journeys | `POST /journeys/{trackingNumber}/scans` | `recordScan` | `200`, the journey after the scan |
| Health | `GET /healthz`, `GET /readyz` | none (not in the spec) | `200`; `/readyz` is `503` while Postgres or Kafka is down or the process drains |

Every POST requires an `Idempotency-Key` header.

## Conventions in the contract

- **Idempotency-Key.** Required on all three POSTs (`internal/adapters/inbound/http/idempotency.go`): at most 128 characters, a repeat with the same key and body replays the first response for 24 hours, the same key with a different body is `422 validation-failed`, a missing key is `400 idempotency-key-required`. A 5xx is never cached.
- **Errors** are RFC 9457 `application/problem+json`; `type` is a slug, the model's `Err` name in kebab-case: `validation-failed` (422), `idempotency-key-required` (400), `not-found` (404), `label-already-issued`, `tracking-number-taken`, `void-after-handover`, `duplicate-scan`, `scan-out-of-order`, `journey-closed`, `illegal-transition`, `concurrent-update` (409), `ship-to-unavailable` (503) and `internal-error` (500). Method and route misses are `method-not-allowed` (405) and `not-found` (404). Causes and remedies are on [Troubleshooting](/docs/operations/troubleshooting).
- **Scans that are stored but not applied.** `scan-out-of-order` and `illegal-transition` answer 409 and still keep the scan with `applied=false`; `duplicate-scan` stores nothing.
- **Request bodies** reject unknown fields (`additionalProperties: false`); timestamps are RFC 3339 UTC.
- **No ship-to address anywhere.** No schema of either contract carries an address field (`TestContractsCarryNoShipToField`). `LabelIssued` carries only an opaque `stopKey` and a postal prefix.
- **Events** are CloudEvents 1.0, structured mode, type `com.retailnetwork.logistics.shipping-and-tracking.<label|journey>.<EventName>`, one transactional outbox, Kafka key = the aggregate id.
- **Auth**: none. The fleet reverted REST, MCP and A2A auth on 2026-09-11, and `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns.

## Code versus spec

- `GET /healthz` and `GET /readyz` are registered by the router (`internal/adapters/inbound/http/server.go`) but absent from `apis/openapi.yaml`.
- A key reused with a different body is `422 validation-failed` here (`idempotencyKeyReused = validationFailed` in `internal/adapters/inbound/http/errors.go`); this service has no `idempotency-key-reused` slug.
