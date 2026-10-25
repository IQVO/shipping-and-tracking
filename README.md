# shipping-and-tracking

Retail network, bounded context **Shipping & Tracking** (Generic, logistics tier).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

Issues shipping labels (the only place outside customer-ordering that ever sees a ship-to address) and owns each package's journey: one customer-visible timeline consolidated from vendor handover, load and delivery facts, ending in delivered, returned or lost.

The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/shipping-and-tracking); `model/model.yaml` there is the single source of truth. This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API. The decisions this repository took where the model is silent are in [`docs/adr`](docs/adr/index.md) (ADR 0002 lists 30 numbered assumptions).

## What it does

- `POST /labels` issues the label of one package, reads the synthetic ship-to from customer-ordering, stores it only as an AES-256-GCM snapshot (purged 30 days after the journey closes), starts the journey and publishes `LabelIssued` with an opaque `stopKey` and a 5-character postal prefix. No response, event or log carries the address.
- `POST /labels/{labelRef}:void` voids a label before the package is handed over.
- `GET /journeys/{trackingNumber}` returns the timeline; `POST /journeys/{trackingNumber}/scans` records a scan.
- `cmd/mcp` exposes the same read to agents: a read-only MCP server (Streamable HTTP, `/mcp`, port 8090) whose one tool `get_journey` returns exactly the REST body of the timeline query; no write tool, no Kafka ([ADR 0006](docs/adr/0006-mcp-server-adoption.md)).
- `web/` is the `shipping_mfe` Module Federation remote for the warehouse-console shell: a read-only journey lookup, journey timeline and scan reference over `GET /journeys/{trackingNumber}` ([ADR 0007](docs/adr/0007-console-remote.md)).
- Consumes vendor, linehaul, last-mile and catalog events, folds them into the journeys, and raises one `NO_SCAN` exception per silence (then `PackageLost` after 48 more hours on an in-transit package).
- Publishes eight CloudEvents 1.0 (`com.retailnetwork.logistics.shipping-and-tracking.<entity>.<Event>`) on `retailnetwork.shipping-and-tracking.events` through a transactional outbox.

Contracts: [`apis/openapi.yaml`](apis/openapi.yaml) and [`apis/asyncapi.yaml`](apis/asyncapi.yaml).

## Layout

| Path | Holds |
| --- | --- |
| `internal/domain/{label,journey,shared}` | pure Go aggregates, no I/O |
| `internal/application` | ports, use cases, views, the ship-to value |
| `internal/adapters/inbound/{http,kafka}` | chi router with the Idempotency-Key and NUL middleware; the four consumers |
| `internal/adapters/outbound/{postgres,kafka,outbox,customerordering,crypto,ids,clock}` | pgx repositories and embedded migrations, CloudEvents encoder and relay sink, the relay, the `GetShipTo` client, AES-GCM and HMAC, ids, clock |
| `internal/adapters/outbound/memory` | test doubles only (`cmd/**` never imports them) |
| `cmd/api` | composition root (HTTP, relay, consumers, jobs) |
| `cmd/mcp` | composition root of the read-only MCP server (ADR 0006) |
| `internal/adapters/inbound/mcp` | the MCP inbound adapter over `GetJourney` (ADR 0006) |
| `web/` | the `shipping_mfe` console remote: Vite + React, federation container `shipping_mfe`, dev/preview port 4020 (ADR 0007) |
| `features/` | godog scenarios |

## Run

There is no in-memory mode. `cmd/api` needs real PostgreSQL and Kafka and refuses to boot when a required variable is missing:
`DATABASE_URL`, `KAFKA_BROKERS`, `CUSTOMER_ORDERING_URL`, `SHIP_TO_ENCRYPTION_KEY` (base64 of 32 bytes), `STOP_KEY_SECRET`
(at least 16 bytes), and the four consumer groups `VENDOR_`, `LINEHAUL_`, `LASTMILE_` and `CATALOG_CONSUMER_GROUP`.
The optional variables and their defaults are in the header of [`cmd/api/main.go`](cmd/api/main.go) and in
[ADR 0005](docs/adr/0005-runtime-configuration-jobs-and-retention.md).

```bash
make check          # gofmt, vet, build, lint, tests with -race
make coverage       # at least 90% across internal/domain and internal/application
make bdd            # the godog scenarios
make integration    # testcontainers Postgres and Kafka, including the real-wiring test of cmd/api (needs Docker)
make mutation       # gremlins over every internal/domain package
```

## Deployment

The Helm chart is [`charts/shipping-and-tracking/`](charts/shipping-and-tracking/): one `api` Deployment (plus Service, ConfigMap, Secrets, ServiceAccount; HPA, Ingress and HTTPRoute are off by default) and two optional components, both off by default: the `mcp` Deployment/Service (ADR 0006, with an opt-in Kong HTTPRoute `/api/mcp/shipping-and-tracking` -> `/mcp`) and the `frontend` nginx Deployment/Service behind the Nginx web gateway at `/mfes/shipping-and-tracking/` (ADR 0007).
`helm template` refuses to render, naming the value, without `database.url` or `database.existingSecret`, `kafka.brokers`,
`config.customerOrderingUrl`, `crypto.existingSecret` (or `crypto.shipToEncryptionKey` and `crypto.stopKeySecret`) and the four
`config.{vendor,linehaul,lastmile,catalog}ConsumerGroup` ids (stable and service-prefixed, never the pod name).
`DATABASE_URL`, `SHIP_TO_ENCRYPTION_KEY` and `STOP_KEY_SECRET` come from Secrets (`secretKeyRef`) and never reach the ConfigMap.
`terminationGracePeriodSeconds` (default 45) must exceed 30s plus `config.shutdownDrainDelay`, the sum of the shutdown bounds in `cmd/api`.
The chart tests run with `bash charts/shipping-and-tracking/tests/render_test.sh` and `python3 charts/shipping-and-tracking/tests/test_service_selectors.py` (needs `helm` and PyYAML).

## The MCP server and the web remote

`cmd/mcp` is a second, independent deployable beside `cmd/api` (ADR 0006): a
read-only MCP server over Streamable HTTP. One tool, `get_journey(trackingNumber)`,
bound to the same `GetJourney` use case the REST adapter serves; errors carry the
REST problem slug (`not-found`). No auth (fleet-wide revert), no Kafka, no relay, no
consumers, no jobs: it opens the service database, runs the idempotent boot
migrations, answers `/healthz` and serves `/mcp`. The advertised tool registry is
pinned byte for byte by a golden file
(`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`).

`web/` is the `shipping_mfe` remote of the warehouse-console shell (ADR 0007):
three read-only surfaces (journey lookup, journey timeline with every stored scan,
scan reference) over this context's one GET. The API base is resolved at runtime
from the console's `/config.json` (`apiOrigin` -> `/api/shipping-and-tracking`); a
production build throws without it. Dev/preview port 4020 (unique across the fleet),
gateway mount `/mfes/shipping-and-tracking/`, image built from `web/Dockerfile`
(`@warehouse/ui-kit` supplied as the `uikit` named BuildKit context). Inside `web/`:
`npm ci && npm run lint && npx tsc -b && npm test && npm run build`.

## Not done yet

The items of [`docs/before-first-release.md`](docs/before-first-release.md) that need infrastructure (repository secrets).
Traces and metrics are not built.
