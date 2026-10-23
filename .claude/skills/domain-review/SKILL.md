---
name: domain-review
description: Ubiquitous-language drift review of a change against .claude/rules/domain-model.md: renamed or invented terms, aggregate-invariant leaks. Invoke explicitly: /domain-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---


Perform a ubiquitous-language drift review of the current changes (or
`$ARGUMENTS` if given), comparing new/changed code against
`.claude/rules/domain-model.md` (or this repo's equivalent doc — check
the ADRs under `docs/adr/` (0002 lists the aggregates and assumptions) if that
file doesn't say enough).

Ubiquitous language drift is the quiet failure mode DDD is supposed to
prevent: code that technically works but silently renames, reshapes, or
duplicates a concept the domain-model doc already named — so future
readers can no longer map code to domain conversation.

## What to check, in priority order

1. **A new type/field/method that duplicates an existing domain concept
   under a different name.** If the domain-model doc already names a
   concept (e.g. this repo's "silence window" — the per-tier time without a scan
   after which the sweep raises an exception), a new calculation that computes the
   same thing under a different name (`QuietPeriod`, `IdleLimit`, etc.)
   is drift — even if the math is correct, it fragments the vocabulary.
   Flag it and point at the existing name.
2. **A domain type/method named in implementation terms instead of
   domain terms.** `internal/domain/` code should read like the ubiquitous
   language, not like database/HTTP vocabulary — a method called
   `UpdateRow` or `PatchState` where the domain-model doc would call the
   equivalent operation `RecordScan`/`ReturnToSender`/`DeclareLost` is drift, and the
   fitness-test suite won't catch this because it's a naming problem, not
   an import-direction problem.
3. **An invariant enforced in code that the domain-model doc doesn't
   mention, or vice versa.** If a new domain rule was added to the code
   (a new validation, a new state-transition guard), the domain-model doc
   should be updated in the SAME PR — an undocumented invariant is
   invisible to the next person who touches that aggregate and may
   accidentally remove it thinking it's dead code.
4. **A value object that should be closed but was implemented open (or
   vice versa).** The journey states, scan types, label tier and carrier
   (`internal/domain/journey`, `internal/domain/label`) are deliberately CLOSED
   enums listed in ADR 0002; check any new categorical field against whether
   the domain wants an open or closed set, and flag a mismatch either direction.
5. **A cross-aggregate rule implemented as a call instead of a local check,
   or vice versa.** `label` and `journey` share the tracking number by value
   only and never call each other; the one outward call is `GetShipTo`, made by
   the use case before the transaction (ADR 0003), never from the domain. A
   change that makes a domain package reach for external state would be a real
   regression even if it still "works".
6. **New terminology introduced without updating the domain-model doc.**
   If new code introduces a genuinely new domain concept the doc doesn't
   yet name, that's not necessarily wrong — but the doc needs a new entry
   in the SAME PR, or the vocabulary silently forks between prose and
   code.

## Output format

For each finding: the term/concept involved, where it appears in the
domain-model doc (or "not yet documented"), where the drift appears in
code, and a one-sentence recommendation (rename, document, or confirm
it's an intentional new concept). If the changes introduce no new domain
concepts and use existing vocabulary correctly, say so plainly.

This command never modifies files. It is advisory input for the author
to act on, not a blocking gate.
