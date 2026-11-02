# ADR 0010: adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Supersedes the idempotency parts of
[ADR 0002](./0002-aggregates-and-invariants.md) A24 (route-scoped key, reuse as 422) and of
[ADR 0005](./0005-runtime-configuration-jobs-and-retention.md) (the `Idempotency-Key` section).

## Context

The fleet standardised its REST contracts: URLs name things (no `:custom-method` suffix, no verb
segment), every POST and PATCH is idempotent with one key semantics, a key reused with another request
is `409`, the replay header is `Idempotency-Replayed`, and every collection is paginated with keyset
`{items,nextCursor}`. This service had one colon route and a route-scoped key reused as 422.

## Decision

- **Route.** `POST /labels/{labelRef}:void` becomes `POST /labels/{labelRef}/voids` (transition noun
  "void -> voids"). `operationId: voidLabel`, the 200 status and the body do not change. The old route is
  gone (`TestRetiredRoutesAreGone`), no alias.
- **Idempotency.** All three POSTs (`/labels`, `/labels/{labelRef}/voids`, `/journeys/{trackingNumber}/scans`)
  stay behind the transactional middleware; there is no PATCH and no unkeyed write.
  - The key is global: migration `0002_idempotency_key_global` makes `key` the primary key (rows
    that shared a key across routes keep the oldest; they live 24 h).
  - Fingerprint = method + path + SHA-256 of the body. Same key, different fingerprint: `409`
    `idempotency-key-reused` (was 422 `validation-failed`).
  - Missing key: `400 idempotency-key-required`; not 1-128 printable ASCII: `400 idempotency-key-invalid`
    (was 422).
  - A replay carries `Idempotency-Replayed: true`; the first execution never does.
  - The key row, the aggregate change and the outbox rows still commit in one transaction; 5xx and
    `concurrent-update` are not stored.
- **Pagination.** The service has no collection GET (`GET /journeys/{trackingNumber}` is one resource whose
  `scans` array is part of it), so nothing is paginated and no `x-pagination-exempt` is declared.
- **Lint.** `.spectral.yaml` runs the `rest-guideline` rule (`spectral lint apis/openapi.yaml --fail-severity=warn`).

## Consequences

- Breaking for callers of the void route and for anything that read 422 as key reuse. vendor-connectivity
  (POST /labels) is unaffected except for the new problem types. No other repo was found calling the void route.
- Must deploy together with the REST cutover set of the fleet.
