package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// IssueLabelCmd is the in-org request of vendor-connectivity (contracts-delta E2).
type IssueLabelCmd struct {
	PONumber     string
	PackageNo    int
	AssignmentID string
	OrderID      string
	Tier         shared.Tier
}

// IssueLabel issues a label and starts the package journey (IssueLabel and
// StartJourney, ADR 0002 A4). The ship-to is read BEFORE the transaction opens,
// so no lock is held across the network.
type IssueLabel struct{ writer }

// NewIssueLabel builds the use case.
func NewIssueLabel(d Deps) *IssueLabel { return &IssueLabel{writer{d}} }

// placeholder values that pass the label validation, used to check the command
// before the ship-to is fetched.
var (
	placeholderStopKey = strings.Repeat("0", label.StopKeyLen)
	placeholderPrefix  = "0"
)

func (c IssueLabelCmd) validate() error {
	_, _, err := label.Issue(label.NewLabel{
		Ref: "x", TrackingNumber: "x", PONumber: c.PONumber, PackageNo: c.PackageNo, AssignmentID: c.AssignmentID,
		OrderID: c.OrderID, Tier: c.Tier, StopKey: placeholderStopKey, DestinationPostalPrefix: placeholderPrefix,
	}, time.Time{})
	return err
}

// Execute issues the label. Errors: shared.ErrValidation (also for an order
// customer-ordering does not know), label.ErrLabelAlreadyIssued,
// label.ErrTrackingNumberTaken (after one retry with a new number),
// ports.ErrShipToUnavailable.
func (u *IssueLabel) Execute(ctx context.Context, cmd IssueLabelCmd) (views.Label, error) {
	if err := cmd.validate(); err != nil {
		return views.Label{}, err
	}
	existing, err := u.Labels.FindByPackage(ctx, cmd.PONumber, cmd.PackageNo)
	if hit, err := found(err); err != nil {
		return views.Label{}, err
	} else if hit {
		return views.Label{}, fmt.Errorf("%w: label %s covers package %d of %s", label.ErrLabelAlreadyIssued,
			existing.Ref(), cmd.PackageNo, cmd.PONumber)
	}
	addr, err := u.ShipTo.GetShipTo(ctx, cmd.OrderID)
	if errors.Is(err, ports.ErrNotFound) {
		return views.Label{}, shared.Invalid("orderId %q is unknown to customer-ordering", cmd.OrderID)
	}
	if err != nil {
		return views.Label{}, err
	}
	return u.issue(ctx, cmd, addr)
}

func (u *IssueLabel) issue(ctx context.Context, cmd IssueLabelCmd, addr shipto.Address) (views.Label, error) {
	if err := addr.Validate(); err != nil {
		return views.Label{}, err
	}
	plain, err := addr.Plaintext()
	if err != nil {
		return views.Label{}, fmt.Errorf("serialize ship-to: %w", err)
	}
	sealed, err := u.Sealer.Seal(plain)
	if err != nil {
		return views.Label{}, fmt.Errorf("seal ship-to: %w", err)
	}
	in := label.NewLabel{
		PONumber: cmd.PONumber, PackageNo: cmd.PackageNo, AssignmentID: cmd.AssignmentID, OrderID: cmd.OrderID,
		Tier: cmd.Tier, StopKey: u.StopKeys.StopKey(addr.Normalized()), DestinationPostalPrefix: addr.PostalPrefix(),
	}
	// One retry with a fresh tracking number answers a generator collision (ADR 0002 A2).
	for attempt := 0; ; attempt++ {
		v, err := transact(ctx, u.UoW, func(ctx context.Context) (views.Label, error) {
			in.Ref, in.TrackingNumber = u.IDs.NewLabelRef(), u.IDs.NewTrackingNumber()
			return u.create(ctx, in, sealed)
		})
		if errors.Is(err, label.ErrTrackingNumberTaken) && attempt == 0 {
			continue
		}
		return v, err
	}
}

func (u *IssueLabel) create(ctx context.Context, in label.NewLabel, sealed repository.SealedShipTo) (views.Label, error) {
	now := u.now()
	l, labelEvents, err := label.Issue(in, now)
	if err != nil {
		return views.Label{}, err
	}
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: in.TrackingNumber, LabelRef: string(in.Ref), PONumber: in.PONumber, AssignmentID: in.AssignmentID,
		OrderID: in.OrderID, Tier: in.Tier,
	}, now)
	if err != nil {
		return views.Label{}, err
	}
	if err := u.Labels.Save(ctx, l); err != nil {
		return views.Label{}, err
	}
	if err := u.Snapshots.Put(ctx, l.Ref(), sealed); err != nil {
		return views.Label{}, err
	}
	if err := u.Journeys.Save(ctx, j); err != nil {
		return views.Label{}, err
	}
	if err := u.publish(ctx, labelEvents); err != nil {
		return views.Label{}, err
	}
	return views.LabelFrom(l), nil
}

// VoidLabel voids a label and closes its LABELLED journey as VOIDED (VoidLabel,
// ADR 0002 A13).
type VoidLabel struct{ writer }

// NewVoidLabel builds the use case.
func NewVoidLabel(d Deps) *VoidLabel { return &VoidLabel{writer{d}} }

// Execute voids the label. A label that is already VOIDED is returned unchanged
// and publishes nothing. Errors: ports.ErrNotFound, shared.ErrValidation,
// journey.ErrVoidAfterHandover.
func (u *VoidLabel) Execute(ctx context.Context, ref label.Ref, reason string) (views.Label, error) {
	return transact(ctx, u.UoW, func(ctx context.Context) (views.Label, error) {
		l, err := u.Labels.Get(ctx, ref)
		if err != nil {
			return views.Label{}, err
		}
		if l.Status() == label.StatusVoided {
			return views.LabelFrom(l), nil
		}
		j, err := u.Journeys.Get(ctx, l.TrackingNumber())
		if err != nil {
			return views.Label{}, err
		}
		now := u.now()
		if err := j.Void(now); err != nil {
			return views.Label{}, err
		}
		events, err := l.Void(reason, now)
		if err != nil {
			return views.Label{}, err
		}
		if err := u.Labels.Save(ctx, l); err != nil {
			return views.Label{}, err
		}
		if err := u.Journeys.Save(ctx, j); err != nil {
			return views.Label{}, err
		}
		if err := u.publish(ctx, events); err != nil {
			return views.Label{}, err
		}
		return views.LabelFrom(l), nil
	})
}
