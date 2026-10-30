# CLAUDE.md draft for shipping-and-tracking

CLAUDE.md and AGENTS.md are write-gated for the build agent. This is the proposed content: copy it to `CLAUDE.md` and
symlink `AGENTS.md -> CLAUDE.md` (the fleet convention). Delete this file afterwards.

---

# shipping-and-tracking

`harness-template: v4`. Retail-network bounded context **Shipping & Tracking** (Generic, logistics tier). A domain-driven
design study, not associated with any company: never name a real company, brand, product or program in code, comments,
ADRs, specs, tests, fixtures or the README.

## What matters

- Two aggregates, `ShippingLabel` (`internal/domain/label`) and `PackageJourney` (`internal/domain/journey`), sharing the
  tracking number by value only. Pure Go: clock and ids are arguments, `Snapshot`/`Restore`, optimistic `version`.
- The ship-to address is synthetic PII (ADR RN-0006, ADR 0002 A9). It is read from customer-ordering before the
  transaction, kept only as an AES-256-GCM snapshot, purged 30 days after the journey closes, and never logged,
  published or returned. `shipto.Address` redacts itself. No OpenAPI or AsyncAPI schema may carry an address field
  (`TestContractsCarryNoShipToField`).
- Decisions where the model is silent are the numbered assumptions A1..A30 of `docs/adr/0002-aggregates-and-invariants.md`.
  Change one only through a new ADR.
- Real PostgreSQL and Kafka only. `cmd/**` never imports `internal/adapters/outbound/memory` (test doubles).
  `DATABASE_URL`, `KAFKA_BROKERS`, `CUSTOMER_ORDERING_URL`, `SHIP_TO_ENCRYPTION_KEY`, `STOP_KEY_SECRET` and the four
  `*_CONSUMER_GROUP` variables are boot-time requirements (ADR 0005).
- CloudEvents 1.0 structured mode, `com.retailnetwork.logistics.shipping-and-tracking.<label|journey>.<Event>`, topic
  `retailnetwork.shipping-and-tracking.events`, one transactional outbox, one inbox (`processed_events`).
- Errors are RFC 9457 problem+json; the slug is the model's Err name in kebab-case. No authentication.

## Gates

`make check` (fmt, vet, build, lint, `go test -race`), `make coverage` (at least 90% over domain and application),
`make bdd`, `make arch-test`, `make mutation` (gremlins over every `internal/domain/*` package, thresholds 90/90),
`make integration` (needs Docker; includes the real-wiring test of `cmd/api`). The pre-push hook runs `make check`.

## Rules for changes

- Contract first: `apis/openapi.yaml` and `apis/asyncapi.yaml`, then ADR 0004's type catalogue in the same commit, then code.
- A new consumed type is written into ADR 0003 (`TestConsumedTypesAreDocumented`).
- Shared `repocontract` tests run against both the memory doubles and Postgres; add a case there, not in one adapter.
- Chart: `charts/shipping-and-tracking/` (api only; tests in `charts/shipping-and-tracking/tests/`, run by the `chart-tests` and `helm-lint` CI jobs). A new env var in `cmd/api` must be rendered by the chart or `render_test.sh` fails.
- Not built: traces and metrics, ship-to key rotation (ADR 0005, `docs/before-first-release.md`).

A2A: `cmd/a2a` (port 8093) is this context's read-only Agent2Agent agent, skills `get_journey` (read), decided in `docs/adr/0009-a2a-inbound-adapter.md`; delegation only (facts stay CloudEvents on Kafka), unauthenticated like REST and MCP; rules in `.claude/rules/fleet/a2a.md`.
