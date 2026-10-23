package usecases

import (
	"context"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// ShipToRetention is how long a ship-to snapshot outlives the close of its
// journey (contracts-delta: purged 30 days after delivered, returned or lost).
const ShipToRetention = 30 * 24 * time.Hour

// PurgeShipTo deletes the ship-to snapshots whose journey closed more than
// ShipToRetention ago (ADR 0002 A9).
type PurgeShipTo struct{ writer }

// NewPurgeShipTo builds the use case.
func NewPurgeShipTo(d Deps) *PurgeShipTo { return &PurgeShipTo{writer{d}} }

// Execute purges and returns how many snapshots it deleted.
func (u *PurgeShipTo) Execute(ctx context.Context) (int, error) {
	return u.Snapshots.PurgeClosedBefore(ctx, u.now().Add(-ShipToRetention))
}

// RecordCatalogItem upserts the CatalogDimensionsView from a consumed
// CatalogItemCreated (ADR 0002 A18).
type RecordCatalogItem struct{ writer }

// NewRecordCatalogItem builds the use case.
func NewRecordCatalogItem(d Deps) *RecordCatalogItem { return &RecordCatalogItem{writer{d}} }

// Execute claims the event and stores the row. It returns false when the event
// had been claimed before.
func (u *RecordCatalogItem) Execute(ctx context.Context, consumer, eventID string, d repository.CatalogDimensions) (bool, error) {
	if !shared.ValidID(d.CatalogItemID) {
		return false, shared.Invalid("catalogItemId %q is not a valid id", d.CatalogItemID)
	}
	return transact(ctx, u.UoW, func(ctx context.Context) (bool, error) {
		claimed, err := u.Inbox.Claim(ctx, consumer, eventID)
		if err != nil || !claimed {
			return false, err
		}
		return true, u.Catalog.Upsert(ctx, d)
	})
}
