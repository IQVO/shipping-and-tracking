package journey

import (
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func (j *Journey) closedErr() error {
	return fmt.Errorf("%w: journey %s is %s", ErrJourneyClosed, j.tracking, j.state)
}

// RaiseException records a NO_SCAN exception in any open state; the state does
// not change (RaiseException).
func (j *Journey) RaiseException(now time.Time) ([]shared.Event, error) {
	if j.state.Terminal() {
		return nil, j.closedErr()
	}
	j.lastExceptedAt = shared.UTC(now)
	j.version++
	return []shared.Event{PackageExceptionRaised{
		Header: shared.Header{Version: j.version, At: j.lastExceptedAt}, TrackingNumber: j.tracking,
		AssignmentID: j.assignmentID, OrderID: j.orderID, Reason: ReasonNoScan, RaisedAt: j.lastExceptedAt,
	}}, nil
}

// ReturnToSender moves an AT_STATION journey to RETURNED (ReturnToSender).
func (j *Journey) ReturnToSender(returnedAt, now time.Time) ([]shared.Event, error) {
	if err := j.closeFrom(StateAtStation, StateReturned, now); err != nil {
		return nil, err
	}
	return []shared.Event{PackageReturned{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		OrderID: j.orderID, ReturnedAt: shared.UTC(returnedAt),
	}}, nil
}

// ReturnUncollected returns a READY_FOR_COLLECTION journey to the sender
// after the pickup hold expired (ADR 0016): the uncollected package re-enters
// the network as reverse freight.
func (j *Journey) ReturnUncollected(returnedAt, now time.Time) ([]shared.Event, error) {
	if err := j.closeFrom(StateReadyForCollection, StateReturned, now); err != nil {
		return nil, err
	}
	return []shared.Event{PackageReturned{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		OrderID: j.orderID, ReturnedAt: shared.UTC(returnedAt), Reason: ReasonUncollected,
	}}, nil
}

// ReasonUncollected is the return reason of an expired pickup hold (ADR 0016).
const ReasonUncollected = "UNCOLLECTED"

// DeclareLost moves an IN_TRANSIT journey to LOST (DeclareLost).
func (j *Journey) DeclareLost(declaredAt, now time.Time) ([]shared.Event, error) {
	if err := j.closeFrom(StateInTransit, StateLost, now); err != nil {
		return nil, err
	}
	return []shared.Event{PackageLost{
		Header: shared.Header{Version: j.version, At: shared.UTC(now)}, TrackingNumber: j.tracking,
		OrderID: j.orderID, DeclaredAt: shared.UTC(declaredAt),
	}}, nil
}

func (j *Journey) closeFrom(from, to State, now time.Time) error {
	if j.state.Terminal() {
		return j.closedErr()
	}
	if j.state != from {
		return fmt.Errorf("%w: journey %s is %s, not %s", shared.ErrIllegalTransition, j.tracking, j.state, from)
	}
	j.state = to
	j.closedAt = shared.UTC(now)
	j.version++
	return nil
}

// Void closes a LABELLED journey as VOIDED (ADR 0002 A13). It raises no event:
// LabelVoided announces the void.
func (j *Journey) Void(now time.Time) error {
	if j.state.Terminal() {
		return j.closedErr()
	}
	if j.state != StateLabelled {
		return fmt.Errorf("%w: journey %s is %s", ErrVoidAfterHandover, j.tracking, j.state)
	}
	j.state = StateVoided
	j.closedAt = shared.UTC(now)
	j.version++
	return nil
}

// unanswered reports whether a NO_SCAN exception was raised and no applied scan
// arrived since.
func (j *Journey) unanswered() bool {
	return !j.lastExceptedAt.IsZero() && !j.lastExceptedAt.Before(j.lastScanAt)
}

// Silent reports whether ExceptionOnSilence must raise NO_SCAN now: a watched
// state, a window with no applied scan, and no exception already raised for
// this silence (ADR 0002 A7).
func (j *Journey) Silent(now time.Time) bool {
	w := SilenceWindow(j.tier, j.state)
	if w == 0 || j.lastScanAt.IsZero() || j.unanswered() {
		return false
	}
	return !now.Before(j.lastScanAt.Add(w))
}

// Lost reports whether the package must be declared lost now: IN_TRANSIT with
// a NO_SCAN exception that stayed unanswered for LostAfterException (ADR 0002 A8).
func (j *Journey) Lost(now time.Time) bool {
	return j.state == StateInTransit && j.unanswered() && !now.Before(j.lastExceptedAt.Add(LostAfterException()))
}
