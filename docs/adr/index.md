# Architecture decision records

The decisions of `shipping-and-tracking`, in the order they were taken. The ADR bodies
are immutable; a changed decision gets a new ADR that supersedes the old one.
The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-shipping-and-tracking-bounded-context.md) | Shipping & Tracking is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | the ShippingLabel and PackageJourney aggregates, their invariants and the assumptions the model left open | Accepted (2026-10-23) |
| [0003](./0003-consumed-contracts-and-local-read-models.md) | consumed contracts, local read models, relationships and the one synchronous call | Accepted (2026-10-23) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-23) |
| [0005](./0005-runtime-configuration-jobs-and-retention.md) | runtime: configuration, periodic jobs and retention of the technical tables | Accepted (2026-10-23) |
| [0006](./0006-mcp-server-adoption.md) | a read-only MCP server | Accepted (2026-10-24) |
| [0007](./0007-console-remote.md) | the console remote (shipping_mfe): a read-only screen over the existing REST | Accepted (2026-10-24) |
| [0008](./0008-fleet-observability-standard.md) | adopt the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-26) |
