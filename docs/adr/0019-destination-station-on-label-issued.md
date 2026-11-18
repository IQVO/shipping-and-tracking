---
id: 0019
title: The delivery station of an area is kept and published as the destination station of LabelIssued
---

# The delivery station of an area is kept and published as the destination station of LabelIssued

## Status

Accepted (2026-11-18). Supersedes ONE part of ADR 0015 and of ADR 0003's
consumed-contract row for `ServiceAreaAssigned`: that `deliveryStationNodeId`
is "read and dropped". Everything else of both stands.

## Context

linehaul-transportation plans a direct lane from a facility to a delivery
station. `LabelIssued` names the destination of a package only by
`destinationPostalPrefix` (ADR 0002 A10), so the consumer cannot tell which
station serves it. The fact exists in the network: node-topology's
`ServiceAreaAssigned` carries `deliveryStationNodeId`, the DELIVERY_STATION
that serves the prefix, and this context already consumes the event (ADR 0015).
It deliberately dropped the field: nothing here used it.

Three ways to get the station to linehaul were weighed:

- **A. Keep the station per postal prefix here, publish it on `LabelIssued`
  (chosen, approved by the user).** The join key is the 5-character prefix the
  label already has. No new call and no change to node-topology.
- B. linehaul consumes `ServiceAreaAssigned` itself. Possible, but it makes
  linehaul keep a second copy of the area table to answer a question this
  context's event can answer, and it ties the lane to the area version that
  linehaul happens to hold, not the one the label was issued under.
- C. linehaul calls node-topology or this context by REST/MCP when it plans.
  Forbidden: a domain context reads a sibling only through its own copy fed by
  events.

## Decision

### The station is stored per postal prefix

`area_modes` gains `delivery_station_node_id TEXT NULL` (migration `0009`);
`repository.AreaMode` gains `DeliveryStationNodeID`. The consumer reads
`deliveryStationNodeId` from `ServiceAreaAssigned` and keeps it. NULL, and the
empty string in the Go type, mean "no station known": the event named none, or
the row predates the migration.

The station is advisory. A value that is not a valid id (`shared.ValidID`) is
treated as absent, never as a poison message: the area's delivery mode and
carrier options of the same event are still wanted. Absent keeps the previous
tolerant reading of the event; the producer's contract makes the field required,
this copy does not.

### The version guard writes an equal version that fills a missing station

`ports.AreaModeView.Apply` writes a row when the event's `assignmentVersion` is
strictly greater than the stored one, OR when it is equal, the stored station
is NULL and the event brings one. An equal version never replaces a station that
is stored, and an equal version that brings none changes nothing (so no rewrite
of an identical row and no `updated_at` churn). An older version is dropped as
before. The memory double and the Postgres adapter apply the same rule and pass
the same contract (`internal/adapters/outbound/repocontract/areas.go`).

### Existing rows fill on the next replay or re-emit

The migration does not backfill: the station is not known to this database.
Rows stored before it keep a NULL station until a `ServiceAreaAssigned` of their
prefix is applied again: a replay of the topic, or a re-emit by node-topology
at the same `assignmentVersion` (the relaxed guard accepts it), or any newer
assignment (which brings its own station). Until then labels of that prefix are
issued without `destinationStationNodeId`, which is the legacy payload.

A replay only helps if the area fold does not skip the ids it claimed before.
The inbox is the pair `(consumer, CloudEvents id)` kept 30 days, and the area
fold claimed as `node-topology`. The area fold therefore claims under its own
identity from now on, `node-topology.area-modes` (`ConsumerAreaModes`), the
precedent of ADR 0018 for `node-topology.node-destinations`: an id claimed
before is new to it, and the version guard makes the replay harmless. The
pickup-point fold keeps `node-topology`. The procedure is in the runbook
("Backfilling the delivery station of the areas").

### `LabelIssued` gains the optional `destinationStationNodeId`

When a label is issued, the use case reads the area view for the label's
destination postal prefix (before the transaction opens, like the ship-to
read) and sets `destinationStationNodeId` on the event. The field is:

- **optional and omitted** when the prefix has no row in the area view, the row
  has no station, or no area view is wired: the label is issued exactly as
  before and the bytes of the event are identical to the ones published before
  this ADR (golden test);
- a **node id only**. No address, no postal code and no name is added; the
  prefix was already on the event;
- **not part of the label aggregate.** It is a fact of the area view at issue
  time, carried by the event and not stored on the label; a void or a restore
  never needs it;
- additive, so the `dataschema` stays `:v1` and the type string is unchanged.

An infrastructure failure of the area view fails the issue (nothing is stored),
as the ship-to read and the carrier choice already do; a missing row is not a
failure.

A **return label** (ADR 0017, ADR 0018) is not given a station: its destination
is a returns node, not a delivery area, and the area view is not read for it.
Its `LabelIssued` is unchanged.

### Pickup-point labels use the area station of the point's prefix

For a pickup label the destination prefix is the point's own prefix (ADR 0016),
and the station is the area view's station of THAT prefix, when known.

**Open question, not decided by the user:** `pickup_points.station_node_id`
(the point's `servingStationNodeId` from `NodeRegistered`) is a second, direct
statement of "which station serves this point". The two should agree; if
node-topology assigns the point's prefix to a different station than the one it
names as serving the point, the label carries the AREA's station, and the point's
own serving station is not used or consulted as a fallback (a prefix with no area
row publishes no station). The alternative is to prefer the point's serving
station for pickup labels, or to fall back to it. That is a product decision on
which fact is authoritative for pickups, and is left to the user.

## Consequences

- `apis/asyncapi.yaml` changes: `ServiceAreaAssigned.deliveryStationNodeId` is
  now consumed, and `LabelIssued` gains an optional property with an example.
  Both are additive. `apis/openapi.yaml` does not change: no REST DTO exposes
  the event payload (the responses of `POST /labels` and the journey reads are
  views of the label and the journey, and none has a station).
- linehaul-transportation can read `destinationStationNodeId` once it chooses
  to; its change is a separate step, and tolerant consumers ignore the field
  before that.
- **Labels issued before the area row has a station carry no station**, and an
  event already published is never amended. The window ends when the replay or
  re-emit above has run.
- The backfill depends on the topic still retaining the `ServiceAreaAssigned` of
  each prefix, or on node-topology emitting one again. A prefix with neither
  stays without a station until it is assigned again.
- A replay with the area fold's new identity re-applies area events older than
  the inbox's 30 days too; the assignment-version guard drops the stale ones.
- If node-topology reassigns a prefix to another station, the next
  `assignmentVersion` replaces the stored station; labels already issued keep the
  station they were issued with.
- No PII is added: a node id names a facility, not a person or an address.
