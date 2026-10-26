// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go and the values of internal/application/repository.
//
// Optimistic concurrency: every Save expects the stored version to be
// aggregate.Version()-1 (ADR 0002 A15); a create (Version() 1) expects that the
// id is not stored. Any mismatch, including an update of a row that no longer
// exists, is ErrConcurrentUpdate. Inside a UnitOfWork every repository joins the
// transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// LabelRepository persists ShippingLabel aggregates.
type LabelRepository interface {
	// Get returns the label, or ErrNotFound.
	Get(ctx context.Context, ref label.Ref) (*label.ShippingLabel, error)
	// FindByPackage returns the label of (poNumber, packageNo), or ErrNotFound.
	// It backs the ErrLabelAlreadyIssued pre-check.
	FindByPackage(ctx context.Context, poNumber string, packageNo int) (*label.ShippingLabel, error)
	// Save persists the label under the concurrency rule of the package
	// comment. A second label for the same (poNumber, packageNo) is
	// label.ErrLabelAlreadyIssued and a second label with the same tracking
	// number is label.ErrTrackingNumberTaken (unique indexes).
	Save(ctx context.Context, l *label.ShippingLabel) error
}

// ShipToSnapshots stores the encrypted ship-to snapshots, one per label.
type ShipToSnapshots interface {
	// Put stores the snapshot of a label. A second Put for the same label is
	// ErrConcurrentUpdate (a snapshot is written once).
	Put(ctx context.Context, ref label.Ref, sealed repository.SealedShipTo) error
	// Has reports whether a snapshot is stored for the label.
	Has(ctx context.Context, ref label.Ref) (bool, error)
	// PurgeClosedBefore deletes the snapshots whose journey reached a terminal
	// state strictly before cutoff and returns how many it deleted.
	PurgeClosedBefore(ctx context.Context, cutoff time.Time) (int, error)
}

// JourneyRepository persists PackageJourney aggregates with their scans.
type JourneyRepository interface {
	// Get returns the journey with its scans in recorded order, or ErrNotFound.
	Get(ctx context.Context, tn shared.TrackingNumber) (*journey.Journey, error)
	// Save persists the journey under the concurrency rule of the package
	// comment, appending the scans the stored row does not have yet (a scan
	// is never updated or deleted).
	Save(ctx context.Context, j *journey.Journey) error
	// ListStale returns one page of the journeys the silence sweep may act on
	// (see repository.StaleFilter); none is an empty list.
	ListStale(ctx context.Context, f repository.StaleFilter) ([]*journey.Journey, error)
}

// CatalogDimensionsView is the local read model of offer-availability's
// CatalogItemCreated (ADR 0003).
type CatalogDimensionsView interface {
	// Upsert stores the row, replacing an earlier one of the same item.
	Upsert(ctx context.Context, d repository.CatalogDimensions) error
	// Get returns the row of an item, or ErrNotFound.
	Get(ctx context.Context, catalogItemID string) (repository.CatalogDimensions, error)
}

// ProcessedEvents is the inbound-consumer idempotency guard (the inbox).
// Claim records (consumer, CloudEvents id) INSIDE the same UnitOfWork as the
// event's effect and reports false when an earlier committed handling already
// claimed it (the caller then skips).
type ProcessedEvents interface {
	Claim(ctx context.Context, consumer, eventID string) (claimed bool, err error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls. The adapter encodes each event into its CloudEvents envelope (the
// id is minted once, at append) and stores one row per event in order; the
// relay drains the rows to Kafka afterwards. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics).
//
//   - fn MUST use the ctx passed to it for every call that should participate.
//   - Do is re-entrant: when ctx already carries an open unit, Do joins it.
//   - The error fn returns is returned from Do unchanged (errors.Is keeps
//     working); a commit failure is returned wrapped.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the opaque ids of new aggregates (ADR 0002 A2).
type IDGenerator interface {
	NewLabelRef() label.Ref
	NewTrackingNumber() shared.TrackingNumber
}

// ShipToGateway is the model's one sync_call: GetShipTo on customer-ordering.
type ShipToGateway interface {
	// GetShipTo returns the ship-to of an order. An unknown order is
	// ErrNotFound; a timeout or any other failure is ErrShipToUnavailable.
	GetShipTo(ctx context.Context, orderID string) (shipto.Address, error)
}

// ShipToSealer encrypts the serialized ship-to into a snapshot.
type ShipToSealer interface {
	Seal(plaintext []byte) (repository.SealedShipTo, error)
}

// StopKeyer derives the opaque stopKey from the normalized address.
type StopKeyer interface {
	StopKey(normalized string) string
}

// EventEncoder is what the Postgres outbox adapter needs to turn domain events
// into stored messages: one already-encoded outbox message per event, in order,
// with the CloudEvents id minted once (internal/adapters/outbound/kafka).
type EventEncoder interface {
	Encode(events ...shared.Event) ([]outbox.Message, error)
}

// LabelMetrics records the business outcome of every label-issue attempt (Tier 2
// of the fleet metrics convention). The IssueLabel use case calls it once per
// attempt: LabelIssued when a label and its journey were stored, LabelRejected
// otherwise, with the problem slug that says why (the `type` suffix the REST
// adapter answers with: "ship-to-unavailable", "label-already-issued", ...).
// A replayed request never reaches the use case (the Idempotency-Key middleware
// answers it), so it is not an attempt. The implementation lives in
// internal/adapters/outbound/telemetry; a nil Deps.Metrics records nothing.
type LabelMetrics interface {
	// LabelIssued counts a label that was issued.
	LabelIssued(ctx context.Context)
	// LabelRejected counts an attempt that issued no label; reason is a stable,
	// low-cardinality slug.
	LabelRejected(ctx context.Context, reason string)
}
