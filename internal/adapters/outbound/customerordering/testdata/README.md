# customer-ordering contract data

`customer-ordering.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/customer-ordering
`develop` (the producer's own file). It is listed in `contracts/pinned.json`, and the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when it differs from the
producer's `develop`. Refresh = copy the producer's file over it, never edit it, then fix what the
contract tests report. Only `GET /orders/{orderId}/ship-to` (operation `getShipTo`) is called by
shipping-and-tracking.

The route answers `oneOf ShipTo | PickupDestination` and 404 `problem+json`. The spec carries no
whole-response example for this route: the fixtures are derived from its schemas — producer example
backfill pending. `ship-to.example.json` is the address answer (the values of the gateway's own
unit-test document), `pickup-destination.example.json` the ADR 0016 pickup answer, and the two
`*.problem.json` files RFC 9457 answers (`Problem` requires type, title, status, detail, instance;
the producer documents no 5xx type, so `unavailable.problem.json` only exercises the schema's
invariants — the gateway maps by HTTP status and never reads the problem body).
`TestExamplesSatisfyPinnedSpec` checks every fixture against the copy; `TestGatewayDecodesSpecExamples`
feeds them through the real gateway call; `TestFieldsWeReadAreSpecified` walks `shipto.Address`'s
json tags against the union of the two answer schemas. Refresh together when the network contract
changes.
