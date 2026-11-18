---
id: 0018
title: The label owner derives the return destination prefix from the destination node
---

# The label owner derives the return destination prefix from the destination node

## Status

Accepted (2026-11-18). Supersedes ONE part of ADR 0017's Decision: who knows
the return destination's prefix. ADR 0017 had the caller send it
("`destinationPostalPrefix` is the RETURN DESTINATION's prefix (the returns
centre CR chose), which ST cannot know", and "The destination prefix on the
label is the request's `destinationPostalPrefix`"). Everything else of
ADR 0017 stands, its addendum of 2026-11-16 included.

## Context

The caller of `POST /return-labels` is the customer-returns context. Its
architecture forbids postal codes: it holds no address data, so it cannot
supply a postal prefix. What it does hold is the id of the node the return
goes to.

Deriving a label's postal prefix is a capability of the context that issues
labels (ADR 0002 A10). One owner per capability: the derivation belongs here,
not in each caller.

The fact needed is already published. node-topology's `NodeRegistered` carries
a required `postalCode` for every node type, and this context already consumes
it (ADR 0016) - but it kept only `PICKUP_POINT` nodes. Cross-context data
reaches this context through the producer's published events into a local read
model; a REST, MCP or A2A lookup of a sibling to resolve a node is not an
option, and node-topology needs no change.

`apis/openapi.yaml` is pinned byte for byte by its consumers and guarded by the
`spec-breaking` gate, and the route exists on `develop` only (it was never
released to `main`).

## Decision

### The request names the destination node

`IssueReturnLabelRequest` gains `destinationNodeId`. The return destination is
named by EXACTLY ONE of `destinationNodeId` and `destinationPostalPrefix`; the
schema says so with a `oneOf` of two `required` sets, and `destinationPostalPrefix`
is marked `deprecated`. Both, or neither, is `422 validation-failed`, like every
other request this API rejects for what it is. A JSON `null` in either property
is refused the same way, never read as "absent".

When `destinationNodeId` is sent, the destination prefix on the label is the
one this context derived for that node. When the deprecated
`destinationPostalPrefix` is sent, the prefix is taken as sent, exactly as
before, and the node view is not read.

### The derivation rule

`shipto.NodePrefix` is the rule of ADR 0002 A10 (`PostalPrefix`,
`PrefixLen = 5`) restricted to what a label prefix may hold, the pattern
`^[A-Z0-9]+$` of the request and of the aggregate: the registered postal code
is upper-cased, every character outside `A-Z` and `0-9` is dropped
(punctuation, whitespace, and letters or digits of other scripts, which
`Alnum` would keep and the label would then reject), and the first five
characters of what is left are the prefix - the whole run when it is shorter.
For a code written in `A-Z`, `0-9` and punctuation the result equals the
ship-to prefix of the same code.

A code that leaves nothing (empty, punctuation only, another script only) gives
an EMPTY prefix. The node is still stored - it is known - and a return label
for it is refused (below). The event is not a poison message: the other folds
of the same event do not depend on the postal code.

### The read model

`NodeDestinationView` (`node_destinations`, migration `0008`) keeps, for every
node type, what a label needs about a node and nothing else: the node id and
the derived prefix. The registered postal code itself is not stored.

- It is a new view beside `PickupPointView`, not a generalisation of it.
  `PickupPointView` keeps a state, deletes a retired point and its status fold
  replaces the whole row; folding every node type into it would change the
  behaviour of pickup labels. `PickupPointView` is untouched by this ADR.
- It has no state column and no delete. `NodeRegistered` upserts the row; the
  status facts (`NodeActivated`, `NodeSuspended`, `NodeRetired`) carry a
  `nodeId` plus an instant or a reason and never reach this view, so they can
  neither demote nor erase a row.
- Whether the node is the right destination, and whether it is active, is the
  caller's policy. This context asks its view for the prefix only.

### One inbox identity per fold

The node-topology consumer keeps its one consumer group. `NodeRegistered` is
now folded twice, each fold in its own unit of work with its own inbox claim:
the older folds keep the identity `node-topology`, the `NodeDestinationView`
fold claims under `node-topology.node-destinations`. A redelivered event id is
skipped by whichever fold already claimed it. The second identity is what
makes the rebuild below possible without touching the inbox: an id the older
folds claimed long ago is still new to this one.

### The refusal

A `destinationNodeId` this context holds no usable prefix for - the node's
registration has not been consumed, or its postal code left nothing - is
refused with **`409 destination-prefix-unavailable`**
(`shipto.ErrDestinationPrefixUnavailable`); nothing is stored, and the detail
says which of the two cases it is. One slug for both: the caller's reaction is
the same.

- Not `422 validation-failed`: the request is well formed, and the very same
  request is served once the node fact has arrived.
- Not `404 not-found`: that answers a resource addressed by the path, and
  "not in this view" does not mean the node does not exist.
- Not `503`: this API answers 503 when a dependency is down
  (`ship-to-unavailable`, `dependency-unavailable`). Nothing is down, and a
  wrong node id sent by a caller must not read as a server failure.
- `409` is what this API answers when a well-formed request is refused because
  of state (`label-already-issued`, `void-after-handover`, `journey-closed`).
  Here the state is this context's own node view.

### Expand, then contract

This is the EXPAND step of a parallel change. Both forms are accepted, so the
change is additive: oasdiff 1.38.0 reports no breaking change (four `info`
entries: a new optional request property, a `oneOf` added to the request body,
`destinationPostalPrefix` became optional, `destinationPostalPrefix`
deprecated).

1. Expand (this ADR): accept `destinationNodeId`, deprecate
   `destinationPostalPrefix`.
2. Migrate: customer-returns sends `destinationNodeId` (its own, separate
   change), after the view has been rebuilt (below).
3. Contract: once no caller sends `destinationPostalPrefix`, a later,
   separately coordinated change removes the property and the `oneOf`, and
   makes `destinationNodeId` required. That one IS a breaking change of the
   spec and goes through the consumers' pins first.

## Consequences

- **The view starts empty.** `NodeRegistered` is emitted once per node, and the
  ones consumed before migration `0008` fed only `pickup_points`. There is no
  replay framework in this repository. The view is rebuilt by pointing the
  node-topology consumer at a fresh consumer group, which re-reads the topic
  from its first retained offset; the procedure is in the runbook
  ("Rebuilding the node destination view"). It depends on the topic still
  retaining each node's `NodeRegistered`: a node whose event is gone cannot be
  restored from the topic and stays refused. Until the rebuild is done and
  verified, no caller should send `destinationNodeId`.
- **Known cost: the refusal is stored under the Idempotency-Key.** The
  middleware stores every answer below 500 except `concurrent-update` and
  replays it to the same key for 24 hours (ADR 0005, ADR 0010). So a
  `destination-prefix-unavailable` answered under a key is replayed to a retry
  of that key even after the node fact has arrived, until housekeeping purges
  the row. A caller whose key is deterministic per (authorization, package)
  cannot get that label under that key before then. This ADR does not change
  what the middleware stores - that rule is being revisited for the whole
  fleet, separately; `TestReturnLabelDestinationRefusalIsStoredAndReplayedUnderItsKey`
  pins the behaviour as it is, and is the test to flip when the rule changes.
- The consumed `NodeRegistered` in `apis/asyncapi.yaml` documents `nodeId` and
  `postalCode` as relied on for every node type. `postalCode` is not made
  required there: a registration without one still makes the node known.
- A `NodeRegistered` without a `nodeId` is now dead-lettered (it names no node);
  before, it was acknowledged without an effect.
- `apis/openapi.yaml` changed, so every pinned copy of it is stale until
  refreshed (vendor-connectivity today; customer-returns when it pins).
- `POST /labels`, the outbound label and its events are unchanged.
