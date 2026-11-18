package usecases

import (
	"context"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

// NoteNodeDestinationCmd is one consumed NodeRegistered of node-topology, of
// any node type (ADR 0018). PostalCode is the node's registered postal code as
// the producer sent it; only the prefix derived from it is kept.
type NoteNodeDestinationCmd struct {
	Consumer   string
	EventID    string
	NodeID     string
	PostalCode string
}

// NoteNodeDestination folds node-topology's NodeRegistered into the
// NodeDestinationView read model: one row per node with the destination prefix
// this context derives from the registered postal code (shipto.NodePrefix).
// The node's lifecycle facts never reach it, so they can neither demote nor
// erase a row.
type NoteNodeDestination struct{ writer }

// NewNoteNodeDestination builds the use case.
func NewNoteNodeDestination(d Deps) *NoteNodeDestination { return &NoteNodeDestination{writer{d}} }

// Execute claims the event id and upserts the node's row in one unit; an id
// already claimed under cmd.Consumer is acknowledged without an effect.
// Errors: infrastructure only.
func (u *NoteNodeDestination) Execute(ctx context.Context, cmd NoteNodeDestinationCmd) error {
	_, err := transact(ctx, u.UoW, func(ctx context.Context) (struct{}, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return struct{}{}, err
		}
		return struct{}{}, u.NodeDestinations.Upsert(ctx, repository.NodeDestination{
			NodeID: cmd.NodeID, PostalPrefix: shipto.NodePrefix(cmd.PostalCode),
		})
	})
	return err
}
