package usecases

import (
	"context"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// ApplyAreaCmd is one consumed node-topology ServiceAreaAssigned (ADR 0015).
type ApplyAreaCmd struct {
	Consumer string
	EventID  string
	Area     repository.AreaMode
}

// ApplyAreaResult says what one area assignment did.
type ApplyAreaResult struct {
	Skipped bool // the CloudEvents id had been claimed before
	Stale   bool // an older-or-equal assignment version: acknowledged, dropped
	Stored  bool
}

// ApplyAreaAssignment folds a ServiceAreaAssigned into the AreaModeView read
// model under the assignment-version guard (the same guard delivery-promise
// uses): an event older than the stored version changes nothing.
type ApplyAreaAssignment struct{ writer }

// NewApplyAreaAssignment builds the use case.
func NewApplyAreaAssignment(d Deps) *ApplyAreaAssignment { return &ApplyAreaAssignment{writer{d}} }

// Execute applies the assignment. Errors: ports.ErrNotFound never (a missing
// row is a store); anything else is infrastructure.
func (u *ApplyAreaAssignment) Execute(ctx context.Context, cmd ApplyAreaCmd) (ApplyAreaResult, error) {
	stored, err := transact(ctx, u.UoW, func(ctx context.Context) (ApplyAreaResult, error) {
		claimed, err := u.Inbox.Claim(ctx, cmd.Consumer, cmd.EventID)
		if err != nil || !claimed {
			return ApplyAreaResult{Skipped: err == nil}, err
		}
		stored, err := u.AreaModes.Apply(ctx, cmd.Area)
		if err != nil {
			return ApplyAreaResult{}, err
		}
		return ApplyAreaResult{Stored: stored, Stale: !stored}, nil
	})
	return stored, err
}
