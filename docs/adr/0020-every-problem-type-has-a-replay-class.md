---
id: 0020
title: Every problem type has a replay class; the not-stored list becomes the class
---

# Every problem type has a replay class; the not-stored list becomes the class

## Status

Accepted (2026-11-19). Adopts the fleet standard of the owner decision of 2026-11-18
(warehouse-harness-template: `.claude/rules/fleet/idempotency-and-outbox.md`,
`docs/idempotency-replay-classes.md`). It replaces, for refusals only, the sentence "responses below
500 are cached, with one exception" of ADR 0005, "5xx and `concurrent-update` are not stored" of ADR
0010, and the list `notStoredUnderKey` introduced by ADR 0018 and extended by ADR 0016. A 2xx is still
stored and replayed. No Decision text of those ADRs is rewritten: each carries a dated pointer here.

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) claims the key row inside the
request transaction, runs the handler behind `SAVEPOINT handler_run` and, for a 4xx, releases the
savepoint, stores the answer and **commits the handler's writes with it**. Until now every answer below
500 was stored for 24 hours except three refusals the handler marked through one list
(`notStoredUnderKey`, `response.go`: `concurrent-update`, `destination-prefix-unavailable`,
`pickup-point-unavailable`). Every other refusal was stored whatever it said, including refusals that
follow data that changes: an order customer-ordering does not know yet, a ship-to address the shopper has
since corrected, a tracking-number collision. Two callers use keys that are deterministic per package
(vendor-connectivity `POST /labels` `vc-label-<po>-<pkg>`, customer-returns `POST /return-labels`), so a
wrongly stored refusal sticks for 24 hours per package.

## Decision

A request retried under the same `Idempotency-Key` is answered from the store only when its first answer is
final for that request. Every problem type has exactly one class, declared on its entry of
`problemCatalogue` (`errors.go`, field `replay`):

- **FINAL**: the answer follows the request, or a fact that never reverts. Stored and replayed with
  `Idempotency-Replayed: true`.
- **OUTCOME**: the command committed state and then answered an error that must be remembered. Stored,
  and the writes commit with the stored answer.
- **RETRYABLE**: nothing was committed and the answer depends on state that can change. Not stored; the
  key row rolls back with the request, so the key is free and a retry executes again.

A 2xx is stored; a 5xx and a panic are never stored (every 5xx entry is RETRYABLE). The middleware holds
no list of slugs and no status range beyond 2xx and 5xx: it reads the class. A catalogue entry without a
class fails `TestEveryCatalogueEntryDeclaresAReplayClass`; there is no default in the catalogue. A refusal
that did not go through `writeError` has no class at run time and is released like a RETRYABLE one, the
safe fallback. When unsure between FINAL and RETRYABLE the class is RETRYABLE.

### Mechanism

`writeError` tells the capturing writer the class of the problem it writes (`MarkReplay`, `capture.go`);
`capture.stored()` is true for a 2xx and for a FINAL or OUTCOME refusal. `runFreshRequest` rolls the whole
transaction back, key row included, and flushes the answer for anything not stored: the branch the 5xx
and the former `DoNotCache` already took, so the three refusals of the old list behave exactly as before
and `capture.DoNotCache`, `notStoredUnderKey` and `passesLater` are gone. A FINAL or OUTCOME refusal keeps
the existing path (release the savepoint, heal it with `ROLLBACK TO SAVEPOINT` when a statement failed on
the server, store, commit). An OUTCOME whose writes were lost to that healing is rolled back and answers
500 like a 2xx whose writes were lost. 2xx, 5xx and FINAL answers are byte-identical to before.

- **A concurrent duplicate** blocked on the first request's key row (`INSERT ... ON CONFLICT DO NOTHING`
  waits for the transaction) sees, when the first ends RETRYABLE, its own insert succeed: it EXECUTES the
  command itself and answers what that execution answers (stored if a 2xx, FINAL or OUTCOME). It is not
  answered with the refusal it waited for. This is what a 5xx of the first already did.
  `TestIdempotency_AConcurrentDuplicateExecutesWhenTheFirstEndsRetryable` pins it.
- **Releasing a key** means a different body under it is a NEW request, not `409 idempotency-key-reused`.
- Problems the middlewares write themselves are outside the table: `idempotency-key-required`,
  `idempotency-key-invalid`, the NUL rejections and `method-not-allowed` answer before the key is claimed,
  `idempotency-key-reused` is written on the replay path, `dependency-unavailable` is `/readyz`, and
  `internal-error` is a 500. None is ever stored.

## The class of every problem type

| Problem type | Status | Class | Reason (code) |
| --- | --- | --- | --- |
| `not-found` | 404 | FINAL | Only the three keyed routes that target an id this service minted: `Labels.Get` (`labels.go:234`, `voidReturn`), `Journeys.Get` (`scans.go:41`, `carrier_events.go:103`). Label refs and tracking numbers are minted here (`labels.go:114,181`) and nothing is ever deleted (the only `DELETE`s are the ship-to snapshots, `idempotency_keys`, published outbox rows and `processed_events`: `postgres/labels.go:206`, `housekeeping.go:23-34`). The slug is not shared with a referenced fact on a keyed route: customer-ordering's `ErrNotFound` becomes `validation-failed` (`labels.go:74-76`, `return_labels.go:95-97`), the pickup point and node views become their own slugs (`labels.go:91-93`, `return_labels.go:138-141`), and a missing area is "not carrier" (`carrier_choice.go:29`). Judgement call 2. |
| `validation-failed` | 422 | RETRYABLE | Mixed slug, one class (judgement call 1): most causes are a field of the request, but `labels.go:74-76` and `return_labels.go:95-97` ("orderId is unknown to customer-ordering"), `labels.go:152-154` (the FETCHED ship-to fails `Validate`), `carrier_events.go:63-65` (`at` in the future: the clock moves) and `scans.go:92` (the area is not CARRIER_HANDOFF: the area view changes) follow data or time that change. |
| `ship-to-unavailable` | 503 | RETRYABLE | A 5xx is never stored. |
| `address-suspect` | 422 | RETRYABLE | The address is NOT in the request: it is read from customer-ordering by `orderId` (`labels.go:73`) and checked at `labels.go:157-159`, before the transaction (ADR 0011), so nothing is committed, and the shopper can correct the address. Fetched data, hence RETRYABLE (judgement call 3). |
| `pickup-point-unavailable` | 422 | RETRYABLE | `labels.go:91-99`: the point is not held or not `ACTIVE` yet; node facts change it. Nothing committed (ADR 0016). Unchanged behaviour. |
| `destination-prefix-unavailable` | 409 | RETRYABLE | `return_labels.go:134-150`: the node registration has not been consumed yet. Nothing committed (ADR 0018). Unchanged behaviour. |
| `tracking-number-taken` | 409 | RETRYABLE | `labels.go:179-188`, `postgres/labels.go:171`: a generator collision that survived one retry. The number is generated, not part of the request, so the next execution draws another. |
| `concurrent-update` | 409 | RETRYABLE | `postgres/labels.go:155,173,191`, `journeys.go:129,161`: another writer won the optimistic lock; the retry reloads. Unchanged behaviour. |
| `label-already-issued` | 409 | FINAL | `labels.go:66-72`, `return_labels.go:82-86`, `postgres/labels.go:165-169`: a label covers the package (a voided one too: the unique indexes carry no status, `0007_label_purpose.up.sql`) and a label is never deleted. Judgement call 4. |
| `void-after-handover` | 409 | FINAL | `journey_close.go:86`: the journey is past `LABELLED`; a state never returns to `LABELLED` (`types.go` `next`). Judgement call 4. |
| `journey-closed` | 409 | FINAL | `journey.go:250-251`, `journey_close.go:11`: DELIVERED, COLLECTED, RETURNED, LOST and VOIDED are absorbing (`types.go:41-47`; every transition checks `Terminal()` first). Judgement call 4. |
| `duplicate-scan` | 409 | FINAL | `journey.go:253-255`: a scan id is applied once and scans are never removed (`journey_scans` unique on `(tracking_number, scan_id)`). Judgement call 4. |
| `scan-out-of-order` | 409 | OUTCOME | `journey.go:256-259` stores the scan unapplied (`j.store(in, false)`, `Changed: true`); `scans.go:64-77` saves the journey and returns `refusal{err: scanErr}` with a nil error, so the unit COMMITS and the 409 follows. The same on `carrier_events.go:147-157`. |
| `illegal-transition` | 409 | OUTCOME | `journey.go:267-270`: the same, for a scan with no edge from the state. The slug is answered by no other keyed route: `label.go:198` is unreachable (a label is `ISSUED` or `VOIDED`, and `VOIDED` is answered before, `labels.go:238-240`), `journey_close.go:71` belongs to the sweeps. |

`TestEverySlugHasOneClassAndItIsTheOneOfTheADR` reads this table and holds it equal to the catalogue, slug
for slug: a changed class is an edit of the code and of this ADR together.

### Why the scan refusals are OUTCOMEs

A re-execution is not the same request. The first execution stored the scan (`journey_scans` row, applied
= false) and committed it; a second one meets `ErrDuplicateScan` (`journey.go:253`) and answers
`409 duplicate-scan` instead of the answer the caller was owed. Releasing the key (RETRYABLE) would also roll
the stored scan back, which is the opposite of the domain rule "an older scan is stored but does not move
the state" (`journey.go:23`). The refusal is therefore remembered and the scan commits with it.
`TestAScanRefusedAfterItWasStoredIsAnOutcomeCommittedAndReplayed` and
`TestACarrierEventRefusedAfterItWasStoredIsAnOutcomeToo` pin it over a real Postgres: the scan row exists
after the refusal, the key row is stored, the retry replays with `Idempotency-Replayed: true` and adds no
row.

### Judgement calls

1. **`validation-failed` is RETRYABLE although most causes are the request's own.** One class per problem
   type; a stored "orderId is unknown" or "the ship-to is invalid" would pin a state customer-ordering
   changes. A malformed body is therefore released too and the retry validates again, which costs nothing.
   The clean fix is its own problem types for the fetched-data causes, a contract change left to the owner.
2. **`not-found` is FINAL**, against the default of "unsure: RETRYABLE": the three conditions of the owner
   rule are checked in code above (ids minted here, nothing deleted, slug not shared with a referenced
   fact on a keyed route). The two keyed callers with deterministic keys never answer it (`/labels` and
   `/return-labels` do not declare 404). If a route that reads a referenced fact is ever added under this
   slug, the class must be revisited.
3. **`address-suspect` is RETRYABLE** because the checked address is fetched, not supplied. It would be FINAL
   if the request carried the address (it never does: no schema carries an address field).
4. **`label-already-issued`, `void-after-handover`, `journey-closed` and `duplicate-scan` are FINAL** though
   they depend on aggregate state, not on the request alone: each follows a fact that cannot revert (a label
   is never deleted, a journey never leaves a terminal state or goes back to `LABELLED`, a scan is never
   removed), so a stored refusal can never become false, and the retry would only repeat it. The owner's
   rule says RETRYABLE when unsure; this is the same reading as status-regression and shopper-exists in
   customer-ordering ADR 0019. Flipping one is one word in `errors.go` and in this table.

## Consequences

- A client that retries after `validation-failed`, `address-suspect`, `tracking-number-taken`,
  `concurrent-update`, `pickup-point-unavailable`, `destination-prefix-unavailable` or a 5xx under the same
  key now gets its retry executed, with no `Idempotency-Replayed` header. The first three are new.
- A caller that reuses a key after such a refusal on purpose and changes the body gets a new request, not
  `409 idempotency-key-reused`.
- Deterministic-key callers (REPORTED, not changed here): vendor-connectivity (`vc-label-<po>-<pkg>` on
  `POST /labels`) and customer-returns (`POST /return-labels`) may treat a 422 as terminal; an unknown order
  or a corrected address now passes on a retry under the same key, so they should retry `validation-failed`
  and `address-suspect` as they retry `pickup-point-unavailable`.
- The MCP server and the A2A agent are read-only: no write surface keeps another store.
- Not changed, reported: the router's NUL guard answers `422 validation-failed` for a NUL in the query of
  `GET /journeys/{trackingNumber}` (`nul.go:31-33`), which the route does not declare (it declares 200 and
  404). It is outside the idempotent routes; the spec and the guard disagree and one of them should move.
- Not changed, reported: the `tracking-number-taken` retry of `IssueLabel` (`labels.go:179-188`) runs a
  second attempt inside the request transaction the first attempt may have aborted (a unique violation);
  whether it answers the intended retry or a 500 is not covered by a test.
