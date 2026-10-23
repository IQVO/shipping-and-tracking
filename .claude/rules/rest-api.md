---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Routes to use cases, kept in sync with `apis/openapi.yaml` by hand (Spectral lints the document, nothing lints this list).

- `POST /labels` -> `IssueLabel` (201; 409 `label-already-issued` / `tracking-number-taken` / `concurrent-update`; 422; 503 `ship-to-unavailable`)
- `POST /labels/{labelRef}:void` -> `VoidLabel` (200; 404; 409 `void-after-handover`; 422)
- `GET /journeys/{trackingNumber}` -> `GetJourney` (200; 404)
- `POST /journeys/{trackingNumber}/scans` -> `RecordScan` (200; 404; 409 `duplicate-scan` / `scan-out-of-order` / `illegal-transition` / `journey-closed`; 422)
- `GET /healthz` (liveness), `GET /readyz` (drain gate, Postgres ping, Kafka metadata; 503 `dependency-unavailable`)

## Conventions

- Error shape: RFC 9457 `application/problem+json` with `type` = the slug (a model Err name in kebab-case, plus `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`). An unmapped error is a 500 `internal-error` whose detail never leaks the cause.
- All three POSTs require `Idempotency-Key` (per route, 24 h replay, stored in the same transaction as the work): missing = 400 `idempotency-key-required`; too long or reused with another body = 422 `validation-failed`. A `concurrent-update` 409 is never cached.
- A malformed body (bad JSON, unknown field, trailing data, an empty body) and a NUL character in a body or query are 422 `validation-failed`; a NUL in the path is 404 `not-found`.
- No schema, request, response or log line carries a ship-to field (`TestContractsCarryNoShipToField`). `shipto.Address` redacts itself when formatted.
- Auth: none, consistent with the fleet (decision of 2026-09-11, `TestNoAuthMiddlewareReintroduced`).
