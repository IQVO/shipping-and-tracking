---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
description: "Build, test and run Shipping & Tracking locally on real Postgres and Kafka, then issue a label and read a journey with curl."
---

# Quickstart

The service has no in-memory mode: `cmd/api` needs a real Postgres, a real Kafka broker, a customer-ordering to answer `GetShipTo`, and two secrets. This page gets you from a clean checkout to a first label and a first journey.

:::note Not captured from a run
The commands and request bodies below are derived from the code and from `apis/openapi.yaml`. The responses shown are the shapes those files define; ids, timestamps and tracking numbers in your run will differ.
:::

## Prerequisites

| Tool | Why |
| --- | --- |
| Go (the version in `go.mod`) | build and test |
| Docker | Postgres for the local run, and every `make integration` test (testcontainers boots its own Postgres and Kafka) |
| `golangci-lint` v2.14.0, `gremlins` v0.6.0, `govulncheck` | only for `make lint`, `make mutation`, `make vuln`; the Makefile prints the exact `go install` line when one is missing |
| Node 20+ | only to build this documentation site (`docs/`) and the console remote (`web/`) |
| A Kafka broker on `localhost:9092` | the platform runs ONE broker for the whole fleet (reachable at `localhost:9092` from the kind cluster); the repo ships no docker-compose Kafka |

## Make targets

All targets are in the `Makefile`; `make help` lists them.

| Target | What it runs |
| --- | --- |
| `make build` | `go build ./...` (compiles all three `cmd/` binaries) |
| `make test` | `go test ./... -race`: unit, httptest and godog scenarios, no Docker |
| `make check` | `fmt-check vet build lint test`; this is what the pre-push hook runs |
| `make check-all` | `check` plus `coverage` (gate 90% over domain and application), `arch-test` and `bdd` |
| `make integration` | `go test -tags=integration ./... -race -count=1`, Docker required |
| `make mutation` | gremlins over each `internal/domain/*` package |

## 1. Postgres

```bash
docker run -d --name stt-pg -p 5432:5432 \
  -e POSTGRES_USER=stt -e POSTGRES_PASSWORD=stt -e POSTGRES_DB=stt \
  postgres:17
```

The service applies its embedded migration (`internal/adapters/outbound/postgres/migrations/0001_schema.up.sql`) at boot, so the database starts empty.

## 2. Kafka

Use the platform broker on `localhost:9092`. Topics are created by the first writer, there is no topic-creation step. The four consumers dial lazily, so an empty broker does not block boot.

## 3. A stand-in for customer-ordering

Issuing a label calls `GET {CUSTOMER_ORDERING_URL}/orders/{orderId}/ship-to` and expects a JSON ship-to document (`name`, `line1`, optional `line2`, `city`, `region`, `postalCode`, two-letter `countryCode`, optional `phone`; `internal/application/shipto/shipto.go`). If you do not have the real customer-ordering running, any HTTP server that answers that path will do, for example:

```python
# stub_customer_ordering.py: synthetic ship-to for any order id
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

BODY = json.dumps({"name": "Ana Example", "line1": "1 Sample Street", "city": "Springfield",
                   "region": "SP", "postalCode": "01310-100", "countryCode": "BR"}).encode()

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.endswith("/ship-to"):
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers()
            self.wfile.write(BODY)
        else:
            self.send_response(404); self.end_headers()

HTTPServer(("127.0.0.1", 9100), H).serve_forever()
```

Run it with `python3 stub_customer_ordering.py`. The address is synthetic; this service never logs, publishes or returns it.

## 4. Run the API

```bash
export DATABASE_URL='postgres://stt:stt@localhost:5432/stt?sslmode=disable'
export KAFKA_BROKERS='localhost:9092'
export CUSTOMER_ORDERING_URL='http://127.0.0.1:9100'
export SHIP_TO_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export STOP_KEY_SECRET="$(openssl rand -hex 16)"
export VENDOR_CONSUMER_GROUP=shipping-and-tracking-vendor
export LINEHAUL_CONSUMER_GROUP=shipping-and-tracking-linehaul
export LASTMILE_CONSUMER_GROUP=shipping-and-tracking-lastmile
export CATALOG_CONSUMER_GROUP=shipping-and-tracking-catalog
go run ./cmd/api
```

Nine variables are mandatory (`DATABASE_URL`, `KAFKA_BROKERS`, `CUSTOMER_ORDERING_URL`, `SHIP_TO_ENCRYPTION_KEY`, `STOP_KEY_SECRET` and the four consumer groups); a missing one stops the process before it binds, naming the variable. The full list with defaults is in [Configuration](/docs/operations/configuration).

The boot log is JSON on stdout. Look for `postgres adapters configured`, `consumer running` (four times), `outbox relay running` and `http server listening` with `addr=:8080`. The Collector at `localhost:4317` is optional: export never blocks.

Check the probes:

```bash
curl -s localhost:8080/healthz
curl -s localhost:8080/readyz
```

`/healthz` answers `{"status":"ok"}`. `/readyz` answers 200 only while Postgres answers a ping and Kafka answers a metadata request; otherwise 503.

## 5. Issue a label

Every POST requires an `Idempotency-Key` header (without it: 400 `idempotency-key-required`).

```bash
curl -s -X POST localhost:8080/labels \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: quickstart-1' \
  -d '{"poNumber":"PO-1001","packageNo":1,"assignmentId":"asg-001","orderId":"ord-001","tier":"NEXT_DAY"}'
```

Expected: `201` with the label metadata (`labelRef`, `trackingNumber`, `carrier`, `poNumber`, `packageNo`, `assignmentId`, `orderId`, `tier`, `status: "ISSUED"`, `issuedAt`) and no address. Repeating the call with the same key replays the first response; a new key for the same package answers 409 `label-already-issued`.

## 6. Read the journey and record a scan

Take the `trackingNumber` from the previous response:

```bash
curl -s localhost:8080/journeys/<trackingNumber>

curl -s -X POST localhost:8080/journeys/<trackingNumber>/scans \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: quickstart-scan-1' \
  -d '{"scanId":"scan-1","nodeId":"node-fc-01","scanType":"PICKUP_DEPARTURE","at":"2026-10-31T12:00:00Z"}'
```

The journey starts in state `LABELLED`; `PICKUP_DEPARTURE` moves it to `HANDED_OVER` and the scan is listed with `applied: true`. In normal operation scans arrive from Kafka (vendor, linehaul and last-mile events); the REST route is the manual path.

The label issue also queued `LabelIssued` in `outbox_events`; the relay publishes it to `retailnetwork.shipping-and-tracking.events` within `OUTBOX_RELAY_INTERVAL` (1 s by default).

## 7. Optional: MCP and A2A

Both are separate, read-only binaries that need only the database:

```bash
go run ./cmd/mcp    # MCP_ADDR :8090, Streamable HTTP, tool get_journey
A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # :8093, Agent Card at /.well-known/agent-card.json
```

Both inherit `DATABASE_URL` from the environment you exported above. See [MCP tools](/docs/mcp/tools) and the [API overview](/docs/api-reference).

## Tests

```bash
make test          # no Docker
make bdd           # godog features
make arch-test     # fitness tests
make integration   # needs Docker
```

The pyramid and the CI jobs are described in [Testing](/docs/development/testing).
