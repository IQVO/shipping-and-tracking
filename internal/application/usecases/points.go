package usecases

import (
	"context"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// NotePointCmd is one consumed node-topology node event that may carry a
// pickup point (ADR 0016).
type NotePointCmd struct {
	Consumer string
	EventID  string
	Point    repository.PickupPoint
	// Retired deletes the row instead of storing it.
	Retired bool
}

// NotePickupPoint folds node-topology's node events into the PickupPointView
// read model: only PICKUP_POINT rows are kept, every other node type is a
// no-op (ADR 0016).
type NotePickupPoint struct{ writer }

// NewNotePickupPoint builds the use case.
func NewNotePickupPoint(d Deps) *NotePickupPoint { return &NotePickupPoint{writer{d}} }

// Execute notes the point. Errors: infrastructure only.
func (u *NotePickupPoint) Execute(ctx context.Context, cmd NotePointCmd) error {
	_, err := transact(ctx, u.UoW, func(ctx context.Context) (struct{}, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return struct{}{}, err
		}
		if cmd.Point.ID == "" { // not a pickup point: acknowledged, nothing to keep
			return struct{}{}, nil
		}
		if cmd.Retired {
			return struct{}{}, u.PickupPoints.Delete(ctx, cmd.Point.ID)
		}
		return struct{}{}, u.PickupPoints.Upsert(ctx, cmd.Point)
	})
	return err
}
