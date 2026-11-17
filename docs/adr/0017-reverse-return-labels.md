---
id: 0017
title: Reverse labels for customer returns (C8)
---

# Reverse labels for customer returns (C8)

## Status

Accepted (2026-11-14). The shipping-and-tracking half of the customer-returns
wave (RN ADR 0012): the reverse label route, the label's purpose and the
return-authorization link. The campaign design names ADR 0015 and migration
0005; those numbers were taken on develop by C4 (0015/0005) and C6 (0016/0006),
so this slice takes the next free pair, 0017 and 0007.

## Context

The customer-returns context (CR) authorizes a shopper's return and must hand
the shopper a carrier label for the package's ride back into the network. CR is
the only price and disposition knower; ST alone derives stop keys, because the
ship-to address never leaves customer-ordering (RN ADR 0006, ADR 0002 A9/A10).
A return package is picked up at the very address the outbound package was
delivered to (or at the pickup point it was collected from), so last-mile must
group the reverse stop with the outbound stop - which happens for free when the
reverse label carries the SAME stop key as the outbound label of the same
order. A return package, however, has no purchase order, no fulfillment
assignment and no customer-visible journey of its own: the (poNumber,
packageNo) identity does not fit, and a second journey for one tracking number
would corrupt the outbound promise judgements.

## Decision

- New in-org route `POST /return-labels` (Idempotency-Key, problem+json), the
  reverse twin of `POST /labels`, called by customer-returns exactly as
  vendor-connectivity calls `POST /labels`. Body:
  `{returnAuthorizationId, orderId, packageNo, destinationPostalPrefix}`.
  `orderId` names the outbound order whose ship-to is the return's pickup
  address; `destinationPostalPrefix` is the RETURN DESTINATION's prefix (the
  returns centre CR chose), which ST cannot know.
- `ShippingLabel` gains `Purpose` (`OUTBOUND`, the default | `RETURN`) and
  `ReturnAuthorizationID` (empty on an outbound label). Persisted in
  `shipping_labels.purpose TEXT NOT NULL DEFAULT 'OUTBOUND'` and
  `shipping_labels.return_authorization_id TEXT NULL`, with
  `CHECK (purpose = 'RETURN' iff return_authorization_id IS NOT NULL)` and
  `CHECK (purpose IN ('OUTBOUND', 'RETURN'))`. A RETURN label carries no
  poNumber and no assignmentId; uniqueness for RETURN labels is one label per
  `(return_authorization_id, packageNo)` - `ErrLabelAlreadyIssued` - and the
  outbound one-per-`(poNumber, packageNo)` rule keeps its meaning through
  partial unique indexes (a plain UNIQUE(po_number, package_no) would collide
  two return labels of different authorizations, whose poNumber is empty).
- The reverse label computes `stopKey` with the SAME secret (`STOP_KEY_SECRET`)
  over the same ship-to - the address customer-ordering returns for `orderId`,
  normalized and keyed exactly like the outbound label's (a pickup-point
  destination keys `pickup-point|<pointId>`) - so it EQUALS the outbound label's
  stop key by construction: a pickup stop groups with the delivery stop. The
  destination prefix on the label is the request's `destinationPostalPrefix`.
  No ship-to snapshot is sealed and no deliverability check runs for a return:
  the address was deliverable when the outbound label was issued, and snapshot
  retention is keyed by the journey this label does not have (A9).
- A reverse label rides the ground service: tier STANDARD, carrier `RN-GROUND`.
- A reverse label does NOT start a `PackageJourney`: the (poNumber,
  packageNo)-shaped identity does not fit and the shopper-facing tracking of a
  return is customer-returns' authorization view, never a journey here. Scans
  of reverse packages stay counted-and-acknowledged unknowns (the existing
  behaviour of every consumer and of `RecordScan`'s 404).
- `LabelIssued` gains optional `purpose` and `returnAuthorizationId` fields,
  omitted on an outbound label; tolerant consumers ignore both. No new event
  type, topic or subscription; the dataschema stays `:v1` because the payload
  change is additive.
- `VoidLabel` voids a RETURN label without a journey (there is none); the
  failure mode "label issued but the shopper never ships" ends with CR
  cancelling the authorization and the label staying voidable by ST.

## Consequences

- CR needs no new grant, topic or consumer; it calls the in-org route with an
  Idempotency-Key and stores labelRef/trackingNumber from the response.
- The database gains two columns and swaps one constraint for two partial
  unique indexes (migration 0007); existing outbound rows default to
  `purpose = 'OUTBOUND'` untouched.
- Last-mile groups a return pickup stop with the outbound delivery stop with no
  change of its own (same stopKey), whenever the pickup slice (CR's PICKUP
  method) is activated.
- The REST reference docs are regenerated in the same commit as the OpenAPI
  change; the AsyncAPI `LabelIssued` payload schema documents the two optional
  fields.

## Addendum 2026-11-16: one response schema per label kind

The Decision above says what a RETURN label carries; it does not say which
REST schema answers it. The first cut reused the outbound `Label` schema and
made `poNumber` and `assignmentId` optional there. That loosened the contract
of `POST /labels`, which consumers pin with both fields required (the
`spec-breaking` gate reported four `response-property-became-optional` errors).
The wire bytes of both kinds are unchanged by this addendum; only the contract
that describes them is.

- `POST /labels` keeps answering `Label`, byte-identical to the schema before
  this ADR: `poNumber` and `assignmentId` required, no `purpose`, no
  `returnAuthorizationId`.
- `POST /return-labels` answers its own schema, `ReturnLabel`: the fields a
  reverse label really has, with `purpose` (always `RETURN`) and
  `returnAuthorizationId` required and neither `poNumber` nor `assignmentId`
  declared.
- `POST /labels/{labelRef}/voids` is the one route that serves both kinds
  (the Decision has `VoidLabel` void a RETURN label, and it is the only route
  into that use case). Its `200` is `oneOf: [Label, ReturnLabel]`: an outbound
  label answers `Label` exactly as before, a reverse label answers
  `ReturnLabel`. The two never overlap, because each requires properties the
  other never carries. Two alternatives were weighed and rejected: refusing a
  RETURN label on this route would contradict the Decision, and a second void
  route for returns would be a new behaviour no caller needs
  (customer-returns does not void).
- In code the two kinds are two view types (`views.Label`, `views.ReturnLabel`)
  and the void use case answers `views.AnyLabel`, the view of the label's
  purpose. `label_contract_test.go` in the HTTP adapter holds the views, the
  spec and the real answers of the three routes together.

Known cost: oasdiff reports the void route's `oneOf` as one WARNING
(`response-body-wrapped-in-one-of-original-preserved`), not an error; a
consumer generated from the older spec keeps working for outbound labels, the
only kind it can hold a `labelRef` of.
