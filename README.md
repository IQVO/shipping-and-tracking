# shipping-and-tracking

Retail network, bounded context **Shipping & Tracking** (Generic, logistics tier).

Issues shipping labels (the only place outside customer-ordering that ever sees a ship-to address) and owns each package's journey: one customer-visible timeline consolidated from vendor handover, load and delivery facts, ending in delivered, returned or lost.

It owns the `Label` and `Journey` aggregates. It does **not** own the vendor handover, the linehaul legs, the last-mile attempts or the catalogue items — those contexts' published events are consumed and folded into the journeys. It is a downstream conformist of those four Open Host Services, and it calls customer-ordering's `GetShipTo` over HTTP at label time. This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking); `model/model.yaml` there is the single source of truth. The decisions this repository took where the model is silent are in [`docs/adr`](docs/adr/index.md) (ADR 0002 lists 30 numbered assumptions).

Runtime: Go, hexagonal, real PostgreSQL and Kafka only, CloudEvents 1.0 on every message (`com.retailnetwork.logistics.shipping-and-tracking.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

This repository has no docs site of its own. Its design documentation lives on the warehouse-docs retail-network pages:

| Area | Pages |
|---|---|
| Retail network | [Network overview](https://iqvo.github.io/warehouse-docs/network) |
| This context | [Index / service facts](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking), [Business context](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/business-context), [Domain events](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/domain-events), [Use cases](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/use-cases) |
| DDD artifact pack | [Ubiquitous language](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/ubiquitous-language), [Context map](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/context-map), [Core domain chart](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/bounded-context-canvas), [Aggregate design canvas](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/aggregate-design-canvas), [EventStorming](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/eventstorming), [Class diagram](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/class-diagram), [Sequence diagrams](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/sequence-diagrams), [ER diagram](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/entity-relationship), [Domain message flow](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking/domain-message-flow) |
| Decisions | [Network ADRs](https://iqvo.github.io/warehouse-docs/network/adr) |

In-tree: [docs/adr/](docs/adr/index.md) (decisions, index), `docs/before-first-release.md` (packaging checklist; the rows that need repository secrets remain for the repo admin).

## Architecture

Hexagonal / ports & adapters. Dependencies point inward only:

```
cmd/api/                     composition root: HTTP, the outbox relay, the four consumers,
                             the jobs (silence sweep, housekeeping) — on real Postgres and
                             real Kafka
cmd/mcp/                     composition root of the read-only MCP server (ADR 0006)
cmd/a2a/                     composition root of the read-only A2A server (ADR 0009)
internal/
  domain/                    label, journey, shared — pure Go aggregates, no I/O
  application/               ports, use cases, views, the ship-to value
  adapters/
    inbound/http/            chi router with the Idempotency-Key and NUL middleware
    inbound/kafka/           the four consumers
    inbound/mcp/             the MCP inbound adapter over GetJourney (ADR 0006)
    inbound/a2a/             the A2A inbound adapter over GetJourney (ADR 0009)
    outbound/postgres/       pgx repositories and embedded migrations
    outbound/kafka/          CloudEvents encoder and relay sink
    outbound/outbox/         the transactional outbox relay
    outbound/customerordering/ the GetShipTo client
    outbound/crypto/         AES-256-GCM and HMAC (ship-to snapshot, stopKey)
    outbound/ids/ clock/     ids, clock
    outbound/memory/         test doubles only (cmd/** never imports them, fitness-tested)
  architecture/              the fitness tests below
apis/                        openapi.yaml (REST) + asyncapi.yaml (events)
docs/adr/                    9 ADRs + index
features/                    8 godog feature files
charts/shipping-and-tracking/ the Helm chart
web/                         the shipping_mfe console remote
```

The layering is an executable test, not a convention: `internal/architecture/architecture_test.go` (`TestHexagonalArchitecture`) fails the build if the domain ever imports an outer layer or an adapter reaches sideways into a sibling; the same package pins the CloudEvents envelope, the event catalogue and the Kafka rules.

## Domain events

All facts leave as CloudEvents 1.0 through the transactional outbox ([ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md)). The type convention is `com.retailnetwork.logistics.shipping-and-tracking.<entity>.<Event>`, for example:

```
com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost
com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued
```

Specified in [`apis/asyncapi.yaml`](apis/asyncapi.yaml) — eight published events, four consumed topics:

| Topic | Direction | Messages |
|---|---|---|
| `retailnetwork.shipping-and-tracking.events` | publishes (via the transactional outbox) | `LabelIssued`, `LabelVoided`, `PackageHandedOver`, `PackageScanned`, `PackageDelivered`, `PackageExceptionRaised`, `PackageReturned`, `PackageLost` |
| `retailnetwork.vendor-connectivity.events` | consumes | `VendorShipmentConfirmed` |
| `retailnetwork.linehaul-transportation.events` | consumes | `LoadDeparted`, `LoadArrived` |
| `retailnetwork.last-mile-delivery.events` | consumes | `PackageOutForDelivery`, `DeliveryAttempted`, `PackageReturnInitiated` |
| `retailnetwork.offer-availability.events` | consumes | `CatalogItemCreated` |

The consumers fold vendor, linehaul, last-mile and catalog events into the journeys and raise one `NO_SCAN` exception per silence (then `PackageLost` after 48 more hours on an in-transit package).

## Running it

There is no in-memory mode. `cmd/api` needs real PostgreSQL and Kafka and refuses to boot when a required variable is missing: `DATABASE_URL`, `KAFKA_BROKERS`, `CUSTOMER_ORDERING_URL`, `SHIP_TO_ENCRYPTION_KEY` (base64 of 32 bytes), `STOP_KEY_SECRET` (at least 16 bytes), and the four consumer groups `VENDOR_`, `LINEHAUL_`, `LASTMILE_` and `CATALOG_CONSUMER_GROUP`.

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/shipping_and_tracking?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export CUSTOMER_ORDERING_URL="http://localhost:8082"
export SHIP_TO_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export STOP_KEY_SECRET="$(openssl rand -hex 16)"
export VENDOR_CONSUMER_GROUP=shipping-and-tracking-vendor
export LINEHAUL_CONSUMER_GROUP=shipping-and-tracking-linehaul
export LASTMILE_CONSUMER_GROUP=shipping-and-tracking-lastmile
export CATALOG_CONSUMER_GROUP=shipping-and-tracking-catalog
go run ./cmd/api    # migrations run at boot; REST on :8080
```

The optional variables and their defaults are in the header of [`cmd/api/main.go`](cmd/api/main.go) and in [ADR 0005](docs/adr/0005-runtime-configuration-jobs-and-retention.md).

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | **required** | Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, consumers, `/readyz`) |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; `log` is a boot error |
| `CUSTOMER_ORDERING_URL` | **required** | Base URL of customer-ordering (`GetShipTo`) |
| `CUSTOMER_ORDERING_TIMEOUT` | `3s` | One `GetShipTo` call |
| `SHIP_TO_ENCRYPTION_KEY` | **required** | Base64 of the 32-byte AES-256-GCM key |
| `STOP_KEY_SECRET` | **required** | HMAC secret of the stopKey, at least 16 bytes |
| `VENDOR_CONSUMER_GROUP` | **required** | Stable group id of the vendor-connectivity consumer |
| `LINEHAUL_CONSUMER_GROUP` | **required** | Stable group id of the linehaul consumer |
| `LASTMILE_CONSUMER_GROUP` | **required** | Stable group id of the last-mile consumer |
| `CATALOG_CONSUMER_GROUP` | **required** | Stable group id of the catalog consumer |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SILENCE_SWEEP_INTERVAL` | `5m` | Exception-on-silence sweep period |
| `HOUSEKEEPING_INTERVAL` | `1h` | Ship-to purge and technical-table purge period |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `shipping-and-tracking` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | telemetry default | Collector OTLP/gRPC endpoint; export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/mcp` (read-only): `MCP_ADDR` (default `:8090`), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`, `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT` plus the telemetry variables. `cmd/a2a` (read-only): `A2A_ADDR` (default `:8093`), `A2A_AGENT_URL` (**required**, the absolute URL its Agent Card advertises), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`, `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT` plus the telemetry variables.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0008](docs/adr/0008-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id` and a `request_id`; `cmd/api`, `cmd/mcp` and `cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter `shipping.labels.issued` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the consumers. The chart's `otel.*` values render the env.

## API

REST contract: [`apis/openapi.yaml`](apis/openapi.yaml). Writes take `Idempotency-Key` (a replay returns the first answer); errors are RFC 9457 problem details, shared problem types `not-found` (404), `illegal-transition` (409), `concurrent-update` (409) and `validation-failed` (422), plus one slug per model `Err` name.

- `POST /labels` issues the label of one package, reads the synthetic ship-to from customer-ordering, stores it only as an AES-256-GCM snapshot (purged 30 days after the journey closes), starts the journey and publishes `LabelIssued` with an opaque `stopKey` and a 5-character postal prefix. No response, event or log carries the address.
- `POST /labels/{labelRef}:void` voids a label before the package is handed over.
- `GET /journeys/{trackingNumber}` returns the timeline.
- `POST /journeys/{trackingNumber}/scans` records a scan.

`GET /healthz` and `GET /readyz` serve the probes.

## Helm chart

The Helm chart is [`charts/shipping-and-tracking/`](charts/shipping-and-tracking/): one `api` Deployment (plus Service, ConfigMap, Secrets, ServiceAccount; HPA, Ingress and HTTPRoute are off by default) and three optional components, all off by default: the `mcp` Deployment/Service ([ADR 0006](docs/adr/0006-mcp-server-adoption.md), with an opt-in Kong HTTPRoute `/api/mcp/shipping-and-tracking` → `/mcp`), the `a2a` Deployment/Service ([ADR 0009](docs/adr/0009-a2a-inbound-adapter.md), with an opt-in Kong HTTPRoute `/api/a2a/shipping-and-tracking` → `/`; `A2A_AGENT_URL` defaults to its in-cluster Service URL, override with `a2a.agentUrl`) and the `frontend` nginx Deployment/Service behind the Nginx web gateway at `/mfes/shipping-and-tracking/` ([ADR 0007](docs/adr/0007-console-remote.md)).

`helm template` refuses to render, naming the value, without `database.url` or `database.existingSecret`, `kafka.brokers`, `config.customerOrderingUrl`, `crypto.existingSecret` (or `crypto.shipToEncryptionKey` and `crypto.stopKeySecret`) and the four `config.{vendor,linehaul,lastmile,catalog}ConsumerGroup` ids (stable and service-prefixed, never the pod name). `DATABASE_URL`, `SHIP_TO_ENCRYPTION_KEY` and `STOP_KEY_SECRET` come from Secrets (`secretKeyRef`) and never reach the ConfigMap. `terminationGracePeriodSeconds` (default 45) must exceed 30s plus `config.shutdownDrainDelay`, the sum of the shutdown bounds in `cmd/api`. The chart tests run with `bash charts/shipping-and-tracking/tests/render_test.sh` and `python3 charts/shipping-and-tracking/tests/test_service_selectors.py` (needs `helm` and PyYAML); CI runs `helm-lint` (`ct lint`) and `chart-tests`.

### Running the MCP server in Kubernetes

`cmd/mcp` is a second, independent deployable beside `cmd/api` ([ADR 0006](docs/adr/0006-mcp-server-adoption.md)): a read-only MCP server over Streamable HTTP. One tool, `get_journey(trackingNumber)`, bound to the same `GetJourney` use case the REST adapter serves; errors carry the REST problem slug (`not-found`). No auth (fleet-wide revert), no Kafka, no relay, no consumers, no jobs: it opens the service database, runs the idempotent boot migrations, answers `/healthz` and serves `/mcp` (`MCP_ADDR`, default `:8090`). The advertised tool registry is pinned byte for byte by a golden file (`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`).

### Running the A2A agent in Kubernetes

`cmd/a2a` is a third deployable ([ADR 0009](docs/adr/0009-a2a-inbound-adapter.md)): a read-only A2A server over JSON-RPC. One skill, `get_journey`, sent as `{"skill":"get_journey","arguments":{"trackingNumber":"..."}}` in a data part, bound to the same `GetJourney` use case; every outcome is answered in the message envelope as data or as an RFC 7807-shaped `problem` with the REST slug and status (`not-found` 404), never as a JSON-RPC error. It boots only with `DATABASE_URL` and `A2A_AGENT_URL` (the absolute URL its Agent Card advertises), runs the idempotent boot migrations, and, like `cmd/mcp`, has no auth, no Kafka, no relay, no consumers and no jobs. A2A carries delegation only: facts still travel as CloudEvents through `cmd/api`.

## Operator micro-frontend (`web/`)

`web/` is the `shipping_mfe` remote of the warehouse-console shell ([ADR 0007](docs/adr/0007-console-remote.md)): three read-only surfaces (journey lookup, journey timeline with every stored scan, scan reference) over this context's one GET. The API base is resolved at runtime from the console's `/config.json` (`apiOrigin` → `/api/shipping-and-tracking`); a production build throws without it. Dev/preview port 4020 (unique across the fleet), gateway mount `/mfes/shipping-and-tracking/`, image built from `web/Dockerfile` (`@warehouse/ui-kit` supplied as the `uikit` named BuildKit context). Inside `web/`:

```sh
npm ci && npm run lint && npx tsc -b && npm test && npm run build
```

## Local development / quality gate

```bash
make check          # gofmt, vet, build, lint, tests with -race (the pre-push gate)
make coverage       # at least 90% across internal/domain and internal/application
make bdd            # the godog scenarios
make integration    # testcontainers Postgres and Kafka, including the real-wiring test of cmd/api (needs Docker)
make mutation       # gremlins over every internal/domain package
```

`make help` lists everything. lefthook wires the same targets as hooks (`lefthook install` once per clone): pre-commit runs `make fmt-check`, `make vet`, `make lint`; pre-push runs `make check`.

## Tests

- **Unit**: `make test` — domain, use cases, handlers, adapters (race-enabled).
- **BDD**: `make bdd` — godog scenarios in `features/` (8 feature files).
- **Integration**: `make integration` — testcontainers Postgres + Kafka, including the real-wiring test of `cmd/api`.
- **Mutation**: `make mutation` (blocking subset) / `make mutation-full` — gremlins over every `internal/domain` package.
- **Architecture fitness**: `make arch-test` — `internal/architecture/`.
- **Web**: lint/typecheck/test/build of `web/` against `warehouse-ui-kit`.

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`, `test`, `bdd`, `integration`, `mutation-fast`, `api-lint`, `vuln`, `helm-lint`, `chart-tests`, `arch-test` and `web` on every push and PR; the exhaustive `mutation` job and the advisory `drift` sensor run on schedule; `docker-publish` pushes the image and `release` tags and publishes the chart.

## Architecture Decision Records

[docs/adr/](docs/adr/index.md) — index plus 9 ADRs (0001–0009: bounded context, aggregates and invariants, consumed contracts and local read models, CloudEvents envelope and type catalogue, runtime configuration/jobs and retention, MCP server adoption, console remote, fleet observability standard, A2A inbound adapter).

## License

[MIT](LICENSE).
