# ADR 0009: an A2A inbound adapter

## Status

Accepted (2026-10-29).

## Context

The fleet's agents delegate goals to the bounded context that owns the answer.
Three transports carry three different things and do not overlap: Kafka with
CloudEvents carries facts, MCP carries an agent's tools, and Agent2Agent (A2A)
carries the delegation of a goal, never a data sync. shipping-and-tracking
already serves its one read use case (`GetJourney`, the Open Host query of
GET /journeys/{trackingNumber}) over REST and, read-only, over MCP
([ADR 0006](./0006-mcp-server-adoption.md)). A delegating agent such as
warehouse-ops-agent asking "where is this package" should reach the same read
through A2A without learning a REST client.

## Decision

- **A third driving adapter, a separate binary.**
  `internal/adapters/inbound/a2a` is built on the official A2A Go SDK
  (`github.com/a2aproject/a2a-go/v2` v2.7.0) and served by `cmd/a2a`:
  JSON-RPC 2.0 over HTTP at `/` and `/a2a` (the fleet's
  `<CTX>_A2A_ENDPOINT` convention), the Agent Card at
  `/.well-known/agent-card.json`, and an open `/healthz`, on `A2A_ADDR`
  (default `:8093`; 8080 is the api and 8090 the mcp server). It is a third
  deployable beside `cmd/api` and `cmd/mcp`, composed like `cmd/mcp` over the
  same Postgres repository and the same use case. The agent is message-only
  (no Task lifecycle): its one skill is a transactional request and answer.
- **The write gate: read-only, one skill.** A skill may change state only if
  the context's MCP surface already offers that write. The MCP charter of
  this context is read-only (ADR 0006) and the use cases offer exactly one
  read, so the agent offers exactly one skill, derived from the MCP tool
  `get_journey` and named after it: `get_journey(trackingNumber)` returns the
  customer-visible journey (state, the label facts, `handedOverAt`,
  `deliveredAt` and every stored scan, applied or not, in recorded order),
  exactly the fields of the REST body. Issuing and voiding labels and
  recording scans stay on REST and on the event consumers; asking the agent
  for one of them is an `unknown-skill` problem. The delegation never widens
  what the context exposes, and a second skill needs a second read in the
  contract first (the same rule as ADR 0006).
- **In-band answers, REST classification.** A skill call carries
  `{"skill": "<id>", "arguments": {...}}` as a data part (or a text part
  holding the same JSON), decoded strictly: unknown fields and trailing data
  are refused, so a misspelt argument cannot be silently ignored. Every
  outcome is answered in the message envelope, never as a JSON-RPC error: a
  data part `{"skill", "data" | "problem"}` plus a one-line text summary.
  A `problem` is RFC 7807-shaped and carries the same slug and status as the
  REST adapter for the same outcome: a journey that does not exist is `404
  not-found`, a value-object rejection `422 validation-failed`, anything else
  `500 internal-error` with a fixed detail (the cause is logged, never
  answered). The adapter adds `400` problems for what REST cannot express:
  `malformed-skill-request`, `unknown-skill` and `invalid-argument`. A
  tracking number that can never name a journey (`shared.TrackingNumber.Valid`
  is false: a NUL character, whitespace, an over-long id) is the `404` REST
  answers for it in the path, without a database round trip; this is also what
  keeps a NUL from reaching a Postgres text parameter (SQLSTATE 22021, a 500),
  as `rejectNUL` does for REST.
- **The ship-to address does not exist here.** The answer is built from the
  same application view as REST and MCP, which has no address field
  (ADR 0002 A9); the skill makes no call to customer-ordering and the card says
  the agent never reveals one. A wire test pins that no answer mentions an
  address.
- **Read-only is structural.** The adapter's `Deps` holds the one read use
  case; the adapter and `cmd/a2a` import no outbox, no event sink, no Kafka
  adapter, no consumer, no worker, no memory double and no REST adapter, and
  name no use case but `GetJourney`. `cmd/a2a` never dials Kafka, never starts
  the outbox relay and wires no unit of work or publisher, so it can never
  insert into the outbox; a fact still leaves only through `cmd/api`.
  `TestA2AAdapterDependencyRule`, `TestA2AServerIsReadOnly` and
  `TestA2ASensorBites` (internal/architecture) pin it, reusing the detectors
  of the MCP sensor.
- **A server only, no sibling calls, no auth.** There is no outbound A2A
  client (`TestA2AHasNoOutboundClient`), so this context never delegates to a
  sibling, and the agent reads this context's own tables only. The endpoints
  are unauthenticated like every REST and MCP endpoint of the fleet (the
  revert of 2026-09-11): the Agent Card declares no security scheme and
  `TestNoAuthMiddlewareReintroduced` still covers the adapter. Access control
  is the in-cluster ClusterIP boundary.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode) and so is
  `A2A_AGENT_URL`, the absolute http(s) URL the Agent Card advertises; either
  missing or malformed is a boot error that names its variable, raised before
  telemetry, any connection and the listener. The binary runs the idempotent
  embedded migrations on start (with `MIGRATIONS_DATABASE_URL` for a direct
  DSN behind PgBouncer), exactly like `cmd/mcp`. `LOG_LEVEL`,
  `SERVICE_VERSION` (also the card's version), `ENVIRONMENT` and the OTel
  variables complete the environment; each skill run opens an `a2a.skill
  get_journey` span carrying the `a2a.skill` attribute.
- **Chart.** An `a2a` Deployment, Service and opt-in Kong HTTPRoute
  (`/api/a2a/shipping-and-tracking`, prefix rewritten to `/`, so the card
  stays reachable under the same prefix), generated from the mcp templates and
  **off by default** (`a2a.enabled`). `A2A_AGENT_URL` defaults to the
  component's in-cluster Service URL on `:8093` and is overridden by
  `a2a.agentUrl`. The render test asserts the Deployment carries every
  variable `cmd/a2a` reads or requires and none of the Kafka, relay,
  consumer, crypto or customer-ordering variables; `a2a.extraEnv` is held to
  the same rules as the api's and the mcp's.

## Consequences

- A delegating agent can ask where a package is with one goal-shaped message
  and receive the same journey a REST caller or an MCP client would.
- The A2A surface can grow only as the read side does: a second skill is an
  amendment of this ADR AND an `apis/openapi.yaml` change first.
- A write skill would need an MCP write first, then the same use case and the
  transactional outbox as REST (the fact still leaves as a CloudEvent), and
  would replace the read-only sensors with an allowlist of that one use case.
- One more Deployment per release that enables it; with `a2a.enabled=false`
  nothing changes in a running cluster.
