package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// rejectionReasons maps a label-issue failure to the problem slug the REST
// adapter answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{shared.ErrValidation, "validation-failed"},
	{ports.ErrShipToUnavailable, "ship-to-unavailable"},
	{label.ErrLabelAlreadyIssued, "label-already-issued"},
	{label.ErrTrackingNumberTaken, "tracking-number-taken"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one label-issue attempt: issued when err is nil, else
// rejected with the reason of err.
func (u *IssueLabel) recordOutcome(ctx context.Context, err error) {
	if err == nil {
		u.metrics().LabelIssued(ctx)
		return
	}
	u.metrics().LabelRejected(ctx, rejectionReason(err))
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
