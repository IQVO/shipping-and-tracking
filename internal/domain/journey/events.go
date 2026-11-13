package journey

import (
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

const entity = "journey"

// TrackingNumber is the root id of a journey (shared with the label).
type TrackingNumber = shared.TrackingNumber

// PackageScanned is raised for every applied scan. State is the journey state
// after the scan.
type PackageScanned struct {
	shared.Header
	TrackingNumber TrackingNumber
	ScanID         string
	NodeID         string
	ScanType       ScanType
	ScannedAt      time.Time
	State          State
}

// EventName returns "PackageScanned".
func (PackageScanned) EventName() string { return "PackageScanned" }

// Entity returns "journey".
func (PackageScanned) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageScanned) SubjectID() string { return string(e.TrackingNumber) }

// PackageHandedOver is raised on LABELLED -> HANDED_OVER (HandoverOnFirstScan).
type PackageHandedOver struct {
	shared.Header
	TrackingNumber TrackingNumber
	PONumber       string
	NodeID         string
	HandedOverAt   time.Time
}

// EventName returns "PackageHandedOver".
func (PackageHandedOver) EventName() string { return "PackageHandedOver" }

// Entity returns "journey".
func (PackageHandedOver) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageHandedOver) SubjectID() string { return string(e.TrackingNumber) }

// PackageDelivered is raised on OUT_FOR_DELIVERY -> DELIVERED.
type PackageDelivered struct {
	shared.Header
	TrackingNumber TrackingNumber
	AssignmentID   string
	OrderID        string
	DeliveredAt    time.Time
}

// EventName returns "PackageDelivered".
func (PackageDelivered) EventName() string { return "PackageDelivered" }

// Entity returns "journey".
func (PackageDelivered) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageDelivered) SubjectID() string { return string(e.TrackingNumber) }

// PackageHandedToCarrier is raised on AT_STATION -> WITH_CARRIER (ADR 0015):
// the physical handoff of a package of a CARRIER_HANDOFF service area to the
// deterministically chosen carrier at the handoff node.
type PackageHandedToCarrier struct {
	shared.Header
	TrackingNumber          TrackingNumber
	AssignmentID            string
	OrderID                 string
	CarrierID               string
	HandoffNodeID           string
	DestinationPostalPrefix string
	HandedAt                time.Time
}

// EventName returns "PackageHandedToCarrier".
func (PackageHandedToCarrier) EventName() string { return "PackageHandedToCarrier" }

// Entity returns "journey".
func (PackageHandedToCarrier) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageHandedToCarrier) SubjectID() string { return string(e.TrackingNumber) }

// PackageExceptionRaised is raised by RaiseException; the state is unchanged.
type PackageExceptionRaised struct {
	shared.Header
	TrackingNumber TrackingNumber
	AssignmentID   string
	OrderID        string
	Reason         string
	RaisedAt       time.Time
}

// EventName returns "PackageExceptionRaised".
func (PackageExceptionRaised) EventName() string { return "PackageExceptionRaised" }

// Entity returns "journey".
func (PackageExceptionRaised) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageExceptionRaised) SubjectID() string { return string(e.TrackingNumber) }

// PackageReturned is raised on AT_STATION -> RETURNED.
type PackageReturned struct {
	shared.Header
	TrackingNumber TrackingNumber
	OrderID        string
	ReturnedAt     time.Time
}

// EventName returns "PackageReturned".
func (PackageReturned) EventName() string { return "PackageReturned" }

// Entity returns "journey".
func (PackageReturned) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageReturned) SubjectID() string { return string(e.TrackingNumber) }

// PackageLost is raised on IN_TRANSIT -> LOST.
type PackageLost struct {
	shared.Header
	TrackingNumber TrackingNumber
	OrderID        string
	DeclaredAt     time.Time
}

// EventName returns "PackageLost".
func (PackageLost) EventName() string { return "PackageLost" }

// Entity returns "journey".
func (PackageLost) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageLost) SubjectID() string { return string(e.TrackingNumber) }

// PackageEtaWindowAssigned is raised by AssignEta; the state is unchanged.
type PackageEtaWindowAssigned struct {
	shared.Header
	TrackingNumber TrackingNumber
	AssignmentID   string
	OrderID        string
	EtaStart       time.Time
	EtaEnd         time.Time
	AssignedAt     time.Time
}

// PackageDeliveryPointFlagged is raised by FlagDeliveryPoint; the state is
// unchanged (ADR 0014).
type PackageDeliveryPointFlagged struct {
	shared.Header
	TrackingNumber TrackingNumber
	AssignmentID   string
	OrderID        string
	Reason         string
	FlaggedAt      time.Time
}

// EventName returns "PackageDeliveryPointFlagged".
func (PackageDeliveryPointFlagged) EventName() string { return "PackageDeliveryPointFlagged" }

// Entity returns "journey".
func (PackageDeliveryPointFlagged) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageDeliveryPointFlagged) SubjectID() string { return string(e.TrackingNumber) }

// EventName returns "PackageEtaWindowAssigned".
func (PackageEtaWindowAssigned) EventName() string { return "PackageEtaWindowAssigned" }

// Entity returns "journey".
func (PackageEtaWindowAssigned) Entity() string { return entity }

// SubjectID returns the trackingNumber.
func (e PackageEtaWindowAssigned) SubjectID() string { return string(e.TrackingNumber) }

var (
	_ shared.Event = PackageScanned{}
	_ shared.Event = PackageEtaWindowAssigned{}
	_ shared.Event = PackageHandedOver{}
	_ shared.Event = PackageDelivered{}
	_ shared.Event = PackageHandedToCarrier{}
	_ shared.Event = PackageExceptionRaised{}
	_ shared.Event = PackageReturned{}
	_ shared.Event = PackageLost{}
)
