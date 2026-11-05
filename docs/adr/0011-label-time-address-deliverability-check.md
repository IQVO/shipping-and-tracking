# ADR 0011: label-time address deliverability check

## Status

Accepted (2026-11-05).

## Context

A failed first delivery attempt costs the network a re-attempt route leg, a
return-to-station cycle and, when the address is the cause, a package that can
never be delivered. Address defects are the leading cause of failed first
attempts in public last-mile research, and this context is the only place in
the retail network where the plaintext ship-to is open outside
customer-ordering: `IssueLabel` reads it from customer-ordering before the
transaction opens (ADR 0002 A9) and immediately derives the stopKey, the postal
prefix and the encrypted snapshot from it.

`shipto.Address.Validate` already rejects structurally invalid addresses
(missing fields, wrong lengths), but an address can pass `Validate` and still
name nobody at nowhere: a recipient of `Test`, a street line with no house
number, a placeholder city.

## Decision

- `IssueLabel` runs a deterministic deliverability evaluation on the plaintext
  address after `Validate` and before anything is derived, sealed or stored.
- A SUSPECT verdict refuses the issuance with `ErrAddressSuspect`
  (`422 address-suspect`). No label, journey, snapshot or event is written.
- The rules are conservative pattern defects, not risk scores, because a false
  refusal blocks fulfilment of a paid order:
  `RECIPIENT_PLACEHOLDER`, `STREET_PLACEHOLDER`, `STREET_MISSING_NUMBER`,
  `CITY_PLACEHOLDER`, `POSTAL_CODE_TOO_SHORT` (fewer than three alphanumerics
  cannot carry a routable postal prefix).
- The refusal detail carries the reason tokens only, never address values —
  the redaction rule of ADR RN-0006 holds: nothing about the address is logged,
  published or returned beyond the tokens.
- No new event: the vendor sees the typed problem on the synchronous label
  call (the documented sync exception, ADR 0002 / contracts-delta E2) and
  escalates the order through the existing exception surfaces. A customer-side
  address-confirmation loop (an event customer-ordering consumes to re-collect
  the address) is deliberately out of scope here.

## Consequences

- The `$17-per-failed-attempt` class of address-caused failures never enters
  the network when the defect is one of the five patterns.
- The check is deliberately not extensible by configuration: a fixed
  vocabulary keeps the refusal auditable and the problem slug honest.
- The postgres and memory repositories are untouched (the check runs before
  the transaction); the OpenAPI `POST /labels` 422 response documents the slug.
