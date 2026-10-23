# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: retail-network-docs `model/model.yaml`; the decisions this repo took where the model is silent
are the numbered assumptions of `docs/adr/0002-aggregates-and-invariants.md` (A1..A30).

## Ubiquitous Language (use these exact names)

- **Tracking number** — the package's public id (`RN` + 16 base-32 characters). The only thing the two aggregates share, by value (A1, A2).
- **Label** (`ShippingLabel`) — the carrier label of ONE package of a purchase order: unique per `(poNumber, packageNo)` and per tracking number.
- **Journey** (`PackageJourney`) — the customer-visible timeline of one package, folded from vendor, linehaul and last-mile facts.
- **Scan** — one stored observation (`scanId`, node, type, instant, `applied`). A scan that is stored but did not move the state has `applied=false`.
- **Service tier** — `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`; picks the carrier (`RN-EXPRESS` or `RN-GROUND`) and the silence window.
- **Silence** — no scan within the tier's window; raises ONE `NO_SCAN` exception per silence; an exception unanswered for 48 h on an IN_TRANSIT package is a lost package.
- **Ship-to** — the synthetic address read from customer-ordering at issue time. PII: kept ONLY as an AES-256-GCM snapshot, purged 30 days after the journey closes, never logged, published or returned (ADR RN-0006, A9).
- **Stop key** — an opaque keyed hash of the normalized ship-to, with the 5-character postal prefix, on `LabelIssued` so last-mile can group stops without an address (A10).

## Aggregates

- **ShippingLabel** (`internal/domain/label`): issued once per package; `ISSUED` to `VOIDED` only; a void is idempotent and is refused once the package was handed over (that rule lives on the journey).
- **PackageJourney** (`internal/domain/journey`): states `LABELLED`, `HANDED_OVER`, `IN_TRANSIT`, `AT_STATION`, `OUT_FOR_DELIVERY`, then the end states `DELIVERED`, `RETURNED`, `LOST`, `VOIDED`. It only moves forward in time; an older scan or one with no edge is stored, not applied; a known `scanId` is ignored; an end state is final.
- Shared kernel: `internal/domain/shared` (ids, `Tier`, `TrackingNumber`, the error sentinels). The domain is pure: clock and ids are arguments, `Snapshot`/`Restore`, optimistic `version`.

## Domain events

- `LabelIssued`, `LabelVoided` (entity `label`, subject = `labelRef`).
- `PackageScanned`, `PackageHandedOver`, `PackageDelivered`, `PackageExceptionRaised`, `PackageReturned`, `PackageLost` (entity `journey`, subject = `trackingNumber`).
- All eight are integration events on `retailnetwork.shipping-and-tracking.events`; none goes to the analytics topic.

## Key use cases (`internal/application/usecases`)

- `IssueLabel` — reads the ship-to BEFORE the transaction (no lock across the network), then issues the label, seals the snapshot and starts the journey in one unit.
- `VoidLabel` — voids the label and closes a LABELLED journey as VOIDED.
- `RecordScan` — REST scan; `ApplyScans` — the same for a consumed event with many packages, claiming the CloudEvents id first (A16).
- `ReturnToSender` — consumed `PackageReturnInitiated`; `GetJourney` — the Open Host query.
- `SweepSilence` — exception on silence and lost; `PurgeShipTo` — snapshot retention; `RecordCatalogItem` — the `catalog_dimensions` read model.
