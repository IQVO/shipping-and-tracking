package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// IssueReturnLabelCmd is the in-org request of customer-returns, the reverse
// twin of IssueLabelCmd (ADR 0017).
type IssueReturnLabelCmd struct {
	ReturnAuthorizationID   string
	OrderID                 string
	PackageNo               int
	DestinationPostalPrefix string
}

func (c IssueReturnLabelCmd) validate() error {
	_, _, err := label.Issue(label.NewLabel{
		Ref: "x", TrackingNumber: "x", PackageNo: c.PackageNo, OrderID: c.OrderID,
		Purpose: label.PurposeReturn, ReturnAuthorizationID: c.ReturnAuthorizationID,
		Tier: returnTier, StopKey: placeholderStopKey, DestinationPostalPrefix: c.DestinationPostalPrefix,
	}, time.Time{})
	return err
}

// returnTier is the service a reverse label rides: the ground service, whose
// carrier is RN-GROUND (ADR 0017).
const returnTier = shared.TierStandard

// IssueReturnLabel issues the reverse label of one package of a return
// authorization (ADR 0017). The ship-to of the ORIGINAL order is read BEFORE
// the transaction opens - exactly as IssueLabel reads it - because the return
// pickup stop is the outbound delivery stop: the stopKey is derived from the
// same normalized address under the same secret, so it equals the outbound
// label's key by construction. No journey is started and no ship-to snapshot
// is sealed: snapshot retention rides the journey a reverse label does not
// have, and shopper-facing return tracking is customer-returns' view.
type IssueReturnLabel struct{ writer }

// NewIssueReturnLabel builds the use case.
func NewIssueReturnLabel(d Deps) *IssueReturnLabel { return &IssueReturnLabel{writer{d}} }

// Execute issues the reverse label. Errors: shared.ErrValidation (also for an
// order customer-ordering does not know), label.ErrLabelAlreadyIssued (one
// label per (returnAuthorizationId, packageNo)), label.ErrTrackingNumberTaken
// (after one retry with a new number), ports.ErrShipToUnavailable. Every call
// is counted once, as issued or as rejected with the reason (Deps.Metrics).
func (u *IssueReturnLabel) Execute(ctx context.Context, cmd IssueReturnLabelCmd) (views.Label, error) {
	v, err := u.execute(ctx, cmd)
	u.recordOutcome(ctx, err)
	return v, err
}

func (u *IssueReturnLabel) execute(ctx context.Context, cmd IssueReturnLabelCmd) (views.Label, error) {
	if err := cmd.validate(); err != nil {
		return views.Label{}, err
	}
	existing, err := u.Labels.FindByReturnPackage(ctx, cmd.ReturnAuthorizationID, cmd.PackageNo)
	if hit, err := found(err); err != nil {
		return views.Label{}, err
	} else if hit {
		return views.Label{}, fmt.Errorf("%w: label %s covers package %d of return %s", label.ErrLabelAlreadyIssued,
			existing.Ref(), cmd.PackageNo, cmd.ReturnAuthorizationID)
	}
	addr, err := u.ShipTo.GetShipTo(ctx, cmd.OrderID)
	if errors.Is(err, ports.ErrNotFound) {
		return views.Label{}, shared.Invalid("orderId %q is unknown to customer-ordering", cmd.OrderID)
	}
	if err != nil {
		return views.Label{}, err
	}
	// The same derivation the outbound label ran: the pickup-point form for a
	// pickup destination, the normalized address otherwise (ADR 0002 A10,
	// ADR 0016, ADR 0017). No deliverability check and no snapshot: the
	// address was deliverable when the outbound label was issued.
	stopKey := u.StopKeys.StopKey(addr.Normalized())
	if addr.Pickup() {
		stopKey = u.StopKeys.StopKey("pickup-point|" + addr.PickupPointID)
	}
	in := label.NewLabel{
		PackageNo: cmd.PackageNo, OrderID: cmd.OrderID, Purpose: label.PurposeReturn,
		ReturnAuthorizationID: cmd.ReturnAuthorizationID, Tier: returnTier,
		StopKey: stopKey, DestinationPostalPrefix: cmd.DestinationPostalPrefix,
	}
	// One retry with a fresh tracking number answers a generator collision
	// (ADR 0002 A2).
	for attempt := 0; ; attempt++ {
		v, err := transact(ctx, u.UoW, func(ctx context.Context) (views.Label, error) {
			in.Ref, in.TrackingNumber = u.IDs.NewLabelRef(), u.IDs.NewTrackingNumber()
			return u.create(ctx, in)
		})
		if errors.Is(err, label.ErrTrackingNumberTaken) && attempt == 0 {
			continue
		}
		return v, err
	}
}

// create issues the reverse label and publishes LabelIssued; no journey, no
// ship-to snapshot (ADR 0017).
func (u *IssueReturnLabel) create(ctx context.Context, in label.NewLabel) (views.Label, error) {
	l, labelEvents, err := label.Issue(in, u.now())
	if err != nil {
		return views.Label{}, err
	}
	if err := u.Labels.Save(ctx, l); err != nil {
		return views.Label{}, err
	}
	if err := u.publish(ctx, labelEvents); err != nil {
		return views.Label{}, err
	}
	return views.LabelFrom(l), nil
}
