---
id: testing
title: Testing
sidebar_label: Testing
description: "The test pyramid of Shipping & Tracking as it is: unit, integration, godog features, repository contract tests, mutation testing, fitness tests, make targets and CI jobs."
---

# Testing

## The pyramid as it is

| Layer | Where | Count today | Needs |
| --- | --- | --- | --- |
| Unit | `*_test.go` beside the code in `internal/domain/{shared,label,journey}`, `internal/application/*`, the adapters and `cmd/*` | the bulk of the suite | nothing |
| Behaviour (godog / Gherkin) | `features/*.feature`, runner `features/features_test.go` (`TestFeatures`), steps beside it | 8 feature files, 39 scenarios (`issue_label` 9, `journey` 11, `silence` 5, `open_host` 5, `void_label` 4, `consumed_arrivals` 2, `ship_to_retention` 2, `issue_label_edges` 1) | nothing; runs on the in-memory doubles |
| Repository contract | `internal/adapters/outbound/repocontract` | one case table run against BOTH the memory doubles (`memory_test.go`) and Postgres (`repositories_integration_test.go`) | Docker for the Postgres run |
| Integration (`-tags=integration`) | 12 `*_integration_test.go` files: `cmd/api/e2e_integration_test.go` (real wiring of the entry point), `cmd/mcp/main_integration_test.go`, `cmd/a2a/main_integration_test.go`, `internal/adapters/inbound/http/{main,idempotency}_integration_test.go`, `internal/adapters/outbound/outbox/{main,relay,relay_cap}_integration_test.go`, `internal/adapters/outbound/postgres/{main,repositories,housekeeping,outbox_trace}_integration_test.go` | | Docker. Each test boots its own Postgres and Kafka with testcontainers (`internal/testing/pgtest`, `internal/testing/kafkatest`); there is no `DATABASE_URL`, no compose file and no skip gate |
| Contract | `apis/openapi.yaml` and `apis/asyncapi.yaml` linted by Spectral (`.spectral.yaml`, `.spectral.asyncapi.yaml`); `TestEventCatalogueMatchesContract` ties the AsyncAPI to ADR 0004; `TestConsumedTypesAreDocumented` ties consumed types to ADR 0003; `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` pins the MCP tool registry; `internal/adapters/inbound/a2a/card_test.go` pins the Agent Card | | nothing, plus Spectral in CI |
| Mutation | gremlins over every `internal/domain/*` package, configuration in `.gremlins.yaml` | efficacy and mutant-coverage thresholds both `90` | `gremlins` v0.6.0 |
| Fitness (architecture) | `internal/architecture` | 14 files, 38 test functions, each guard paired with a "sensor bites" test that proves it fails on a bad fixture | nothing |
| Web remote | `web/src/**/*.test.ts(x)` with Vitest | console screens, API client, config | Node 22, `warehouse-ui-kit` checked out next to the repo |
| Harness | `scripts/harness/test_hook.py`, `test_repo_lint.py`, `test_red_issue.py` | agent hooks and repo lint | Python 3 |

## Fitness tests

`internal/architecture` guards the design with executable rules:

- `TestHexagonalArchitecture` (domain imports nothing from application or adapters; application imports no adapter), `TestMCPAdapterDependencyRule`, `TestMCPServerIsReadOnly`, `TestA2AAdapterDependencyRule`, `TestA2AServerIsReadOnly`, `TestA2AHasNoOutboundClient`.
- `TestRuntimeBinaryHasNoInMemoryAdapters`: nothing under `cmd/` imports `internal/adapters/outbound/memory`.
- `TestNoGoFilesAtRepoRoot`: godog and everything else lives in subdirectories.
- `TestNoAuthMiddlewareReintroduced`: no authentication in REST, MCP or A2A.
- `TestCloudEventsOnly` and `TestNoEventEnvelopeToggleOrFlatEnvelope`: every Kafka message is a CloudEvent in structured mode.
- `TestEventCatalogueMatchesContract`, `TestConsumedTypesAreDocumented`: events, AsyncAPI and ADR 0003/0004 agree.
- `TestContractsCarryNoShipToField`: no OpenAPI or AsyncAPI schema may carry a ship-to address field.
- `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`, `TestKafkaConsumerGroupNeverHardcodedInline`, `TestReplayConsumersSetCommitInterval`, `TestDeadLetterCallsGuardCancellation`, `TestOutboxRelaysPassAttemptCap`: Kafka and outbox wiring rules.
- `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers`: integration tests never depend on an ambient database or broker.
- `TestMCPOptionalToolInputsCarryOmitempty`: optional MCP tool inputs are optional in the JSON schema.

Run them with `make arch-test`.

## Make targets

| Target | Command | Notes |
| --- | --- | --- |
| `make check` | `fmt-check vet build lint test` | the pre-commit and pre-push gate (lefthook) |
| `make test` | `go test ./... -race` | unit, httptest and godog, no Docker |
| `make coverage` | `go test ./... -race -coverprofile=coverage.out -coverpkg=./internal/domain/...,./internal/application/...` | fails below 90% |
| `make bdd` | `go test ./... -run TestFeatures -v` | godog scenarios |
| `make arch-test` | `go test ./internal/architecture/... -v` | fitness tests |
| `make integration` | `go test -tags=integration ./... -race -count=1` | Docker required; not part of `check` |
| `make mutation` | gremlins on each `internal/domain/*` package | fast blocking gate |
| `make mutation-full` | gremlins on `./internal/domain` with `--workers 1 --timeout-coefficient 30` | exhaustive, scheduled in CI |
| `make vuln` | `govulncheck ./...` | needs `govulncheck` |
| `make check-all` | `check coverage arch-test bdd` | run before pushing |
| `make check-fast` | `fmt-check vet arch-test` plus the packages changed against `HEAD` | agent Stop hook |
| `make guide-lint`, `make harness-test` | agent-guide lint and hook tests | harness sensors |

Chart tests are not Make targets: run `bash charts/shipping-and-tracking/tests/render_test.sh` and `python3 charts/shipping-and-tracking/tests/test_service_selectors.py` (needs `helm` and PyYAML).

## CI jobs (`.github/workflows/ci.yml`)

| Job | What it runs | Blocks `docker-publish` |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | yes |
| `guide-lint` | `scripts/harness/guide_lint.py`, `test_hook.py`; `continue-on-error` | no |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif through golangci-lint plus an informational gocyclo report | yes |
| `test` | build, vet, `make coverage` (90% gate), uploads `coverage.out` | yes |
| `bdd` | `go test ./... -run TestFeatures -v` | yes |
| `integration` | `go test -tags=integration ./... -race -count=1` | yes |
| `mutation-fast` | gremlins on every domain package | yes |
| `mutation` | exhaustive gremlins, on schedule or manual dispatch; opens or closes a `harness:red` issue | no |
| `drift` | dead code, `go mod tidy -diff`, knip on `web/`, coverage quality; weekly, advisory | no |
| `api-lint` | Spectral on both specs, fail on warning | yes |
| `vuln` | govulncheck | yes |
| `arch-test` | fitness tests | yes |
| `helm-lint` | `ct lint` and `test_service_selectors.py`, pull requests only | no |
| `chart-tests` | every `charts/shipping-and-tracking/tests/*.sh` and `test_*.py`; a test that prints `SKIP` fails the job | no |
| `web` | `npm ci`, lint, `tsc -b`, tests and build of `web/` | no |
| `docker-publish`, `release` | image, signature, SBOM, Helm chart and GitHub release; push to `main` only | n/a |

The docs build is its own workflow, `.github/workflows/docs.yml` (build on pull request, deploy on push to `main`). There is no `docs-api-drift` job in `ci.yml`; the generated reference under `docs/docs/api-reference/rest` is regenerated with `npm run gen-api-docs` from `docs/`.
