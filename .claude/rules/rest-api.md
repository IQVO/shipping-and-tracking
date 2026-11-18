---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Routes to use cases, kept in sync with `apis/openapi.yaml` by hand (Spectral lints the document, nothing lints this list). What IS tested is the spec against the code: every typed error of the model has an entry in the problem catalogue (`errors.go`) or is named as not an HTTP answer, and every route declares in `apis/openapi.yaml` every problem it can answer and none it cannot (`problem_parity_test.go`, `problem_typed_errors_test.go`). A new `Err...` or a new route fails there until it is classified.

- `POST /labels` -> `IssueLabel` (201; 409 `label-already-issued` / `tracking-number-taken` / `concurrent-update`; 422 `validation-failed` / `address-suspect` / `pickup-point-unavailable` (the order's pickup point is not held ACTIVE in the local PickupPointView, ADR 0016); 503 `ship-to-unavailable`)
- `POST /return-labels` -> `IssueReturnLabel` (201; 409 `label-already-issued` (one per (returnAuthorizationId, packageNo)) / `destination-prefix-unavailable` (no usable prefix for `destinationNodeId` in the local NodeDestinationView) / `tracking-number-taken` / `concurrent-update`; 422 (also both or neither of `destinationNodeId` and the deprecated `destinationPostalPrefix`); 503 `ship-to-unavailable`; ADR 0017, ADR 0018)
- `POST /labels/{labelRef}/voids` -> `VoidLabel` (200; 404; 409 `void-after-handover` / `journey-closed` / `concurrent-update`; 422)
- `GET /journeys/{trackingNumber}` -> `GetJourney` (200; 404)
- `POST /journeys/{trackingNumber}/scans` -> `RecordScan` (200; 404; 409 `duplicate-scan` / `scan-out-of-order` / `illegal-transition` / `journey-closed`; 422)
- `GET /healthz` (liveness), `GET /readyz` (drain gate, Postgres ping, Kafka metadata; 503 `dependency-unavailable`)
- Not served by `cmd/api`: the read-only `GET /reports/...` routes (tag `reports`, `cmd/st-reports`, `internal/adapters/inbound/reportshttp`, ADR 0012; rules in `.claude/rules/analytics.md`).

## Conventions

- Error shape: RFC 9457 `application/problem+json` with `type` = the slug (a model Err name in kebab-case, plus `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`). An unmapped error is a 500 `internal-error` whose detail never leaks the cause.
- All three POSTs require `Idempotency-Key` (global key, 24 h replay, stored in the same transaction as the work): missing = 400 `idempotency-key-required`; malformed (not 1-128 printable ASCII) = 400 `idempotency-key-invalid`; reused with another method, path or body = 409 `idempotency-key-reused`. A `concurrent-update` 409 is never cached, nor is `destination-prefix-unavailable` (the one list `notStoredUnderKey` in `response.go`; ADR 0018): the key row rolls back and the same key retries the work.
- A malformed body (bad JSON, unknown field, trailing data, an empty body) and a NUL character in a body or query are 422 `validation-failed`; a NUL in the path is 404 `not-found`.
- No schema, request, response or log line carries a ship-to field (`TestContractsCarryNoShipToField`). `shipto.Address` redacts itself when formatted.
- Auth: none, consistent with the fleet (decision of 2026-09-11, `TestNoAuthMiddlewareReintroduced`).
