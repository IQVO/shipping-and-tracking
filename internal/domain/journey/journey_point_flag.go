package journey

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// ErrPointFlagStale: a flag whose flaggedAt is not strictly newer than the
// stored one is not applied (ADR 0014). The consumer acknowledges it.
var ErrPointFlagStale = errors.New("delivery-point flag stale")

// FlagDeliveryPoint stores the delivery-point flag of the package's stop
// (ADR 0014). Like AssignEta it is not a scan: the state does not change. A
// terminal journey is refused (ErrJourneyClosed) and a flaggedAt that is not
// strictly newer than the stored one is refused (ErrPointFlagStale). Order of
// checks: validation, closed, stale.
func (j *Journey) FlagDeliveryPoint(reason string, flaggedAt, now time.Time) ([]shared.Event, error) {
	if reason != "NOT_HOME" && reason != "ACCESS_PROBLEM" {
		return nil, shared.Invalid("reason must be NOT_HOME or ACCESS_PROBLEM")
	}
	if flaggedAt.IsZero() {
		return nil, shared.Invalid("flaggedAt is required")
	}
	if j.state.Terminal() {
		return nil, j.closedErr()
	}
	if !j.pointFlaggedAt.IsZero() && !flaggedAt.After(j.pointFlaggedAt) {
		return nil, fmt.Errorf("%w: %s is not newer than the stored %s", ErrPointFlagStale,
			shared.UTC(flaggedAt).Format(time.RFC3339), j.pointFlaggedAt.Format(time.RFC3339))
	}
	j.pointFlagReason, j.pointFlaggedAt = reason, shared.UTC(flaggedAt)
	j.version++
	return []shared.Event{PackageDeliveryPointFlagged{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		AssignmentID: j.assignmentID, OrderID: j.orderID, Reason: j.pointFlagReason, FlaggedAt: j.pointFlaggedAt,
	}}, nil
}

// PointFlagReason returns the stored delivery-point flag reason ("" if none).
func (j *Journey) PointFlagReason() string { return j.pointFlagReason }

// PointFlaggedAt returns when the delivery point was flagged (zero if none).
func (j *Journey) PointFlaggedAt() time.Time { return j.pointFlaggedAt }
