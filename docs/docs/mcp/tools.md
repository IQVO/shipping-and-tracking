---
id: tools
title: MCP tools
sidebar_label: MCP tools
description: "The MCP server of Shipping & Tracking: transport, endpoint, the single read-only tool get_journey with its input and output schema."
---

# MCP tools

`cmd/mcp` is a separate, read-only deployable (ADR 0006). It serves the Model Context Protocol over **Streamable HTTP only** (no stdio, no SSE) on `MCP_ADDR` (default `:8090`), at `/mcp` and at `/`; `GET /healthz` is the probe. It is **unauthenticated** like REST and A2A: access control is the in-cluster Service boundary. Through Kong it is published at `/api/mcp/shipping-and-tracking` (the prefix is rewritten to `/mcp`).

The binary wires one read use case (`GetJourney`, the same one REST uses) over the Postgres repository and nothing else: no write use case, no outbox, no Kafka. Writes (`POST /labels`, `POST /labels/{labelRef}:void`, `POST /journeys/{trackingNumber}/scans`) stay REST-only. `TestMCPServerIsReadOnly` and `TestMCPAdapterDependencyRule` in `internal/architecture` fail the build otherwise, and the tool-registry test fails on any write-verb tool name.

:::note Source of this page
The tool list is read from `internal/adapters/inbound/mcp/tools.go` and from the golden registry `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`. `TestToolRegistryGolden` compares that file with the server's real `tools/list` response, and `TestToolSurface` pins the exact set (one tool, no write verb in its name). It was not captured from a running pod.
:::

## Tools

| Tool | Annotation | Purpose |
| --- | --- | --- |
| `get_journey` | read-only (`readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false`) | The customer-visible journey of one package |

There is exactly one tool, and no write tool.

### `get_journey`

Reads the state of a package and every stored scan, in the order they were recorded.

**Input** (`additionalProperties: false`)

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `trackingNumber` | string | yes | the tracking number of the package |

**Output** (structured content, camelCase, same shape as the REST `Journey` body)

| Field | Type | Meaning |
| --- | --- | --- |
| `trackingNumber` | string | echo of the key |
| `state` | string | `LABELLED`, `HANDED_OVER`, `IN_TRANSIT`, `AT_STATION`, `OUT_FOR_DELIVERY`, `DELIVERED`, `RETURNED`, `LOST` or `VOIDED` |
| `poNumber`, `assignmentId`, `orderId` | string | identity of the purchase order, fulfillment assignment and customer order of the label |
| `tier` | string | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` or `STANDARD` |
| `handedOverAt`, `deliveredAt` | string (RFC 3339), omitted until they happen | handover and delivery instants |
| `scans` | array | every stored scan, never null in practice (the schema allows `null` or an array) |
| `scans[].scanId` | string | idempotency id of the scan |
| `scans[].nodeId` | string | node where it was seen |
| `scans[].scanType` | string | one of the eight scan types |
| `scans[].at` | string (RFC 3339) | when |
| `scans[].applied` | boolean | `false` for a scan stored out of order or without an edge from the state it met |

**Errors.** A handler error becomes an `isError` tool result (never a protocol failure) whose text is `<slug>: <detail>`, using the REST slugs: an unknown tracking number is `not-found: ...`. Any other failure is logged (`mcp tool failed with an unexpected error`) and returned only as `internal-error: an unexpected internal error occurred`, so no DSN or SQL reaches the model (`internal/adapters/inbound/mcp/errors.go`).

No ship-to address, in any form, is part of the output (ADR 0002 A9).

## Calling it

Any MCP client over Streamable HTTP works. With the cluster route:

```text
POST /api/mcp/shipping-and-tracking
tools/call  name=get_journey  arguments={"trackingNumber": "<tracking number>"}
```

Related: the same read is available as the A2A skill `get_journey` on `cmd/a2a`, described on the [API overview](/docs/api-reference), and as `GET /journeys/{trackingNumber}` on REST.
