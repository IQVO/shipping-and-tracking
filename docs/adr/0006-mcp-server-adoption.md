# ADR 0006: a read-only MCP server

## Status

Accepted (2026-10-24).

## Context

Every bounded context of the fleet that exposes an HTTP API also ships an MCP
server so that agents (warehouse-ops-agent first) can read its data without a
bespoke REST client. shipping-and-tracking had none. The service has exactly
one read use case behind its REST adapter (`GetJourney`, the Open Host query
of GET /journeys/{trackingNumber}), so an MCP adapter is a second driving
adapter over it, not new domain: the rest of the contract is writes (issue a
label, void a label, record a scan) and internal jobs.

## Decision

- **A second driving adapter, a separate binary.**
  `internal/adapters/inbound/mcp` is built on the official MCP Go SDK
  (`github.com/modelcontextprotocol/go-sdk` v1.8.0) and served over Streamable
  HTTP only (no stdio, no SSE) by `cmd/mcp`, mounted at `/mcp` and `/` with an
  open `/healthz`. It is a second deployable beside `cmd/api`; the Helm chart
  gets an mcp Deployment, Service and opt-in Kong HTTPRoute
  (`/api/mcp/shipping-and-tracking` rewritten to `/mcp`).
- **One read tool over the existing query, nothing else.**
  `get_journey(trackingNumber)` returns exactly the REST body of GET
  /journeys/{trackingNumber}: the state, the label facts, handedOverAt and
  deliveredAt, and every stored scan, applied or not, in recorded order.
  Arguments and results are lowerCamelCase like the REST contract; typed
  errors carry the REST problem slug (`not-found`) as the prefix of the tool
  error. Issuing and voiding labels and recording scans stay REST-only.
- **Read-only is structural.** The adapter's `Deps` holds the one read use
  case and nothing that can write; the adapter and `cmd/mcp` import no
  outbox, no event sink, no Kafka adapter, no consumer, no worker and no REST
  adapter. `cmd/mcp` never dials Kafka and never runs the consumers, the
  relay, the sweep or the housekeeping: those stay in `cmd/api`.
  `TestMCPServerIsReadOnly` (ported to internal/architecture) pins the
  imports, the `Deps` fields and the use cases `cmd/mcp` may name;
  `TestToolSurface` pins the exact tool set (budget 1), the read-only
  annotations and the absence of write verbs in tool names;
  `TestToolRegistryGolden` pins the whole advertised registry; the
  integration test proves the binary reads what the write side persisted in a
  real Postgres and adds no outbox row.
- **No sibling calls, no auth.** The server reads this context's own tables
  only, calls no other context over REST or MCP (it never calls
  customer-ordering's GetShipTo), and has no auth layer (the fleet-wide
  revert of 2026-09-11). Access control is the in-cluster ClusterIP boundary.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode); the binary runs
  the idempotent embedded migrations on start (with
  `MIGRATIONS_DATABASE_URL` for a direct DSN behind PgBouncer), exactly like
  `cmd/api`, so it can start against a fresh database before `cmd/api` has.
  `MCP_ADDR` (default `:8090`), `LOG_LEVEL`, `SERVICE_VERSION` and
  `ENVIRONMENT` complete the environment.

## Consequences

- Agents can answer "where is package RN0123456789ABCDEF and what has it been
  through" without REST glue, using the same read the shoppers' timeline
  uses.
- Adding a tool is a reviewed act: the tool budget, the curated list and the
  golden file all change together (an amendment of this ADR).
- The MCP surface can grow only as the read side does: a second read (a
  labels query, say) is an ADR-level change here AND an openapi.yaml change
  first.
