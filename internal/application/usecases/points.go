package usecases

import (
	"context"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// NotePointCmd is one consumed NodeRegistered of node-topology (ADR 0016).
// Point is the pickup point it registers; the zero Point is a node of another
// type, acknowledged without an effect.
type NotePointCmd struct {
	Consumer string
	EventID  string
	Point    repository.PickupPoint
}

// NotePointStatusCmd is one consumed status fact of a node (NodeActivated,
// NodeSuspended, NodeRetired): the node and the state it entered. The fact
// carries nothing else, and nothing else is written.
type NotePointStatusCmd struct {
	Consumer string
	EventID  string
	NodeID   string
	State    string
}

// NotePickupPoint folds node-topology's node events into the PickupPointView
// read model: only PICKUP_POINT rows are kept (ADR 0016). A registration
// writes the descriptive data of a point and never its state once the point
// is held; a status fact writes the state of a point the view holds and
// nothing else (ADR 0016, addendum of 2026-11-18).
type NotePickupPoint struct{ writer }

// NewNotePickupPoint builds the use case.
func NewNotePickupPoint(d Deps) *NotePickupPoint { return &NotePickupPoint{writer{d}} }

// Execute notes a registration: the point's serving station and prefix are
// stored; a point the view already holds keeps its state (a registration
// applied again, in a rebuild, restores the data and demotes nothing).
// Errors: infrastructure only.
func (u *NotePickupPoint) Execute(ctx context.Context, cmd NotePointCmd) error {
	_, err := transact(ctx, u.UoW, func(ctx context.Context) (struct{}, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return struct{}{}, err
		}
		if cmd.Point.ID == "" { // not a pickup point: acknowledged, nothing to keep
			return struct{}{}, nil
		}
		return struct{}{}, u.PickupPoints.Register(ctx, cmd.Point)
	})
	return err
}

// ExecuteStatus notes a status fact: the state of the point changes, its
// station and prefix do not. A node the view holds no row of is not a pickup
// point - a node's registration precedes its status facts on the partition,
// both being keyed by the node id - so the fact is claimed and acknowledged
// without an effect and no row is made (the rule of an unknown tracking
// number, ADR 0002 A17). The same holds for a RETIRED point, which no later
// fact revives. Errors: infrastructure only.
func (u *NotePickupPoint) ExecuteStatus(ctx context.Context, cmd NotePointStatusCmd) error {
	_, err := transact(ctx, u.UoW, func(ctx context.Context) (struct{}, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return struct{}{}, err
		}
		_, err = u.PickupPoints.SetState(ctx, cmd.NodeID, cmd.State)
		return struct{}{}, err
	})
	return err
}
