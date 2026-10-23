package label

import "github.com/claudioed/shipping-and-tracking/internal/domain/shared"

const entity = "label"

// LabelIssued is raised by Issue. Its fields are the model's data list plus
// orderId (ADR 0002 A12), stopKey and destinationPostalPrefix (E10). It never
// carries an address.
type LabelIssued struct {
	shared.Header
	LabelRef                Ref
	TrackingNumber          shared.TrackingNumber
	PONumber                string
	AssignmentID            string
	OrderID                 string
	PackageNo               int
	Tier                    shared.Tier
	Carrier                 string
	StopKey                 string
	DestinationPostalPrefix string
}

// EventName returns "LabelIssued".
func (LabelIssued) EventName() string { return "LabelIssued" }

// Entity returns "label".
func (LabelIssued) Entity() string { return entity }

// SubjectID returns the labelRef.
func (e LabelIssued) SubjectID() string { return string(e.LabelRef) }

// LabelVoided is raised by Void.
type LabelVoided struct {
	shared.Header
	LabelRef       Ref
	TrackingNumber shared.TrackingNumber
	Reason         string
}

// EventName returns "LabelVoided".
func (LabelVoided) EventName() string { return "LabelVoided" }

// Entity returns "label".
func (LabelVoided) Entity() string { return entity }

// SubjectID returns the labelRef.
func (e LabelVoided) SubjectID() string { return string(e.LabelRef) }

var (
	_ shared.Event = LabelIssued{}
	_ shared.Event = LabelVoided{}
)
