package journey

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// ErrEtaStale: an ETA whose assignedAt is not strictly newer than the stored
// one is not applied (ADR 0013). The consumer acknowledges it.
var ErrEtaStale = errors.New("eta window stale")

// AssignEta stores the day-of ETA window of the package's stop (AssignEta). It
// is not a scan: the state does not change. A terminal journey is refused
// (ErrJourneyClosed) and an assignedAt that is not strictly newer than the
// stored one is refused (ErrEtaStale). Order of checks: validation, closed,
// stale.
func (j *Journey) AssignEta(start, end, assignedAt, now time.Time) ([]shared.Event, error) {
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return nil, shared.Invalid("etaStart must be before etaEnd")
	}
	if assignedAt.IsZero() {
		return nil, shared.Invalid("assignedAt is required")
	}
	if j.state.Terminal() {
		return nil, j.closedErr()
	}
	if !j.etaAssignedAt.IsZero() && !assignedAt.After(j.etaAssignedAt) {
		return nil, fmt.Errorf("%w: %s is not newer than the stored %s", ErrEtaStale,
			shared.UTC(assignedAt).Format(time.RFC3339), j.etaAssignedAt.Format(time.RFC3339))
	}
	j.etaStart, j.etaEnd, j.etaAssignedAt = shared.UTC(start), shared.UTC(end), shared.UTC(assignedAt)
	j.version++
	return []shared.Event{PackageEtaWindowAssigned{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		AssignmentID: j.assignmentID, OrderID: j.orderID, EtaStart: j.etaStart, EtaEnd: j.etaEnd, AssignedAt: j.etaAssignedAt,
	}}, nil
}

// EtaCurrent reports whether the stored window is the one to show: the state is
// OUT_FOR_DELIVERY and the window was assigned at or after the last applied
// scan. A newer OUT_FOR_DELIVERY scan makes yesterday's window stale, a failed
// attempt (back to AT_STATION) hides it and no terminal state shows it.
func (j *Journey) EtaCurrent() bool {
	return j.state == StateOutForDelivery && !j.etaAssignedAt.IsZero() && !j.etaAssignedAt.Before(j.lastScanAt)
}

// EtaStart returns the start of the stored ETA window (zero if none).
func (j *Journey) EtaStart() time.Time { return j.etaStart }

// EtaEnd returns the end of the stored ETA window (zero if none).
func (j *Journey) EtaEnd() time.Time { return j.etaEnd }

// EtaAssignedAt returns when the stored ETA window was assigned (zero if none).
func (j *Journey) EtaAssignedAt() time.Time { return j.etaAssignedAt }
