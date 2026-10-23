// Package label is the ShippingLabel aggregate: the label of one package,
// identified by labelRef. It carries the tracking number, the service tier,
// the carrier and two values derived from the ship-to address (an opaque
// stopKey and the postal prefix). The address itself is NOT a field of the
// aggregate: an encrypted snapshot lives behind the ShipToSnapshots port
// (ADR 0002 A9).
//
// States: ISSUED, VOIDED; transition ISSUED -> VOIDED (VoidLabel). Invariants:
// one label per (poNumber, packageNo) (ErrLabelAlreadyIssued) and a unique
// trackingNumber (ErrTrackingNumberTaken); both are raised by the repository
// on its unique indexes.
package label

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// The ShippingLabel invariants of the model, by the model's names.
var (
	// ErrLabelAlreadyIssued: one label per (poNumber, packageNo) (409).
	ErrLabelAlreadyIssued = errors.New("label already issued")
	// ErrTrackingNumberTaken: a tracking number is unique network-wide (409).
	ErrTrackingNumberTaken = errors.New("tracking number taken")
)

// Ref identifies a label (labelRef).
type Ref string

// Status is the lifecycle state of a label.
type Status string

// The two label states.
const (
	StatusIssued Status = "ISSUED"
	StatusVoided Status = "VOIDED"
)

// Bounds shared by the REST contract and the Postgres columns.
const (
	// MaxPackageNo is the Postgres integer ceiling for a package number.
	MaxPackageNo = 2147483647
	// MaxReason is the longest accepted void reason, in characters.
	MaxReason = 500
	// MaxPostalPrefix is the longest destination postal prefix.
	MaxPostalPrefix = 16
	// StopKeyLen is the length of a stopKey (hex HMAC-SHA-256).
	StopKeyLen = 64
)

// The two carriers (ADR 0002 A3).
const (
	CarrierExpress = "RN-EXPRESS"
	CarrierGround  = "RN-GROUND"
)

// CarrierFor returns the carrier chosen for a tier: the two fast tiers ride
// the express carrier, the other two the ground carrier.
func CarrierFor(t shared.Tier) string {
	if t == shared.TierSameDay || t == shared.TierNextDay {
		return CarrierExpress
	}
	return CarrierGround
}

// ShippingLabel is the aggregate root.
type ShippingLabel struct {
	ref          Ref
	tracking     shared.TrackingNumber
	poNumber     string
	packageNo    int
	assignmentID string
	orderID      string
	tier         shared.Tier
	carrier      string
	stopKey      string
	postalPrefix string
	status       Status
	issuedAt     time.Time
	voidReason   string
	voidedAt     time.Time
	version      int64
}

// Snapshot is the persisted form of a label. Restore validates it.
type Snapshot struct {
	Ref                     Ref
	TrackingNumber          shared.TrackingNumber
	PONumber                string
	PackageNo               int
	AssignmentID            string
	OrderID                 string
	Tier                    shared.Tier
	Carrier                 string
	StopKey                 string
	DestinationPostalPrefix string
	Status                  Status
	IssuedAt                time.Time
	VoidReason              string
	VoidedAt                time.Time
	Version                 int64
}

// NewLabel is the input of Issue.
type NewLabel struct {
	Ref                     Ref
	TrackingNumber          shared.TrackingNumber
	PONumber                string
	PackageNo               int
	AssignmentID            string
	OrderID                 string
	Tier                    shared.Tier
	StopKey                 string
	DestinationPostalPrefix string
}

func checkNew(in NewLabel) error {
	if !shared.ValidID(string(in.Ref)) {
		return shared.Invalid("labelRef %q is not a valid id", in.Ref)
	}
	if !in.TrackingNumber.Valid() {
		return shared.Invalid("trackingNumber %q is not a valid id", in.TrackingNumber)
	}
	for _, f := range [][2]string{{"poNumber", in.PONumber}, {"assignmentId", in.AssignmentID}, {"orderId", in.OrderID}} {
		if !shared.ValidID(f[1]) {
			return shared.Invalid("%s %q is not a valid id", f[0], f[1])
		}
	}
	if in.PackageNo < 1 || in.PackageNo > MaxPackageNo {
		return shared.Invalid("packageNo must be between 1 and %d", MaxPackageNo)
	}
	if !in.Tier.Valid() {
		return shared.Invalid("tier %q is not SAME_DAY, NEXT_DAY, TWO_DAY or STANDARD", in.Tier)
	}
	return checkDerived(in.StopKey, in.DestinationPostalPrefix)
}

// checkDerived validates the two values derived from the address.
func checkDerived(stopKey, prefix string) error {
	if !validHex(stopKey, StopKeyLen) {
		return shared.Invalid("stopKey must be %d lower-case hex characters", StopKeyLen)
	}
	if !validPrefix(prefix) {
		return shared.Invalid("destinationPostalPrefix must be 1 to %d characters from A-Z 0-9", MaxPostalPrefix)
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isLowerHex(c byte) bool { return c >= 'a' && c <= 'f' }

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isDigit(c) && !isLowerHex(c) {
			return false
		}
	}
	return true
}

func validPrefix(s string) bool {
	if s == "" || len(s) > MaxPostalPrefix {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isDigit(c) && !isUpper(c) {
			return false
		}
	}
	return true
}

// Issue creates an ISSUED label at version 1 and raises LabelIssued. The
// carrier is chosen from the tier.
func Issue(in NewLabel, now time.Time) (*ShippingLabel, []shared.Event, error) {
	if err := checkNew(in); err != nil {
		return nil, nil, err
	}
	l := &ShippingLabel{
		ref: in.Ref, tracking: in.TrackingNumber, poNumber: in.PONumber, packageNo: in.PackageNo,
		assignmentID: in.AssignmentID, orderID: in.OrderID, tier: in.Tier, carrier: CarrierFor(in.Tier),
		stopKey: in.StopKey, postalPrefix: in.DestinationPostalPrefix, status: StatusIssued,
		issuedAt: shared.UTC(now), version: 1,
	}
	ev := LabelIssued{
		Header: shared.Header{Version: l.version, At: l.issuedAt}, LabelRef: l.ref, TrackingNumber: l.tracking,
		PONumber: l.poNumber, AssignmentID: l.assignmentID, OrderID: l.orderID, PackageNo: l.packageNo,
		Tier: l.tier, Carrier: l.carrier, StopKey: l.stopKey, DestinationPostalPrefix: l.postalPrefix,
	}
	return l, []shared.Event{ev}, nil
}

// Void moves an ISSUED label to VOIDED and raises LabelVoided. A VOIDED label
// is an illegal transition here; the use case answers a repeat void with the
// unchanged label before it gets this far (ADR 0002 A13).
func (l *ShippingLabel) Void(reason string, now time.Time) ([]shared.Event, error) {
	if !shared.ValidTextN(reason, MaxReason) {
		return nil, shared.Invalid("reason must be 1 to %d characters, trimmed, without control characters", MaxReason)
	}
	if l.status != StatusIssued {
		return nil, fmt.Errorf("%w: label %s is %s", shared.ErrIllegalTransition, l.ref, l.status)
	}
	l.status = StatusVoided
	l.voidReason = reason
	l.voidedAt = shared.UTC(now)
	l.version++
	ev := LabelVoided{
		Header: shared.Header{Version: l.version, At: l.voidedAt}, LabelRef: l.ref, TrackingNumber: l.tracking, Reason: reason,
	}
	return []shared.Event{ev}, nil
}

// Restore rebuilds a persisted label, validating every invariant.
func Restore(s Snapshot) (*ShippingLabel, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("label %q: %v", s.Ref, err)
	}
	return &ShippingLabel{
		ref: s.Ref, tracking: s.TrackingNumber, poNumber: s.PONumber, packageNo: s.PackageNo,
		assignmentID: s.AssignmentID, orderID: s.OrderID, tier: s.Tier, carrier: s.Carrier,
		stopKey: s.StopKey, postalPrefix: s.DestinationPostalPrefix, status: s.Status,
		issuedAt: shared.UTC(s.IssuedAt), voidReason: s.VoidReason, voidedAt: shared.UTC(s.VoidedAt), version: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	in := NewLabel{
		Ref: s.Ref, TrackingNumber: s.TrackingNumber, PONumber: s.PONumber, PackageNo: s.PackageNo,
		AssignmentID: s.AssignmentID, OrderID: s.OrderID, Tier: s.Tier, StopKey: s.StopKey,
		DestinationPostalPrefix: s.DestinationPostalPrefix,
	}
	if err := checkNew(in); err != nil {
		return err
	}
	if s.Carrier != CarrierFor(s.Tier) {
		return fmt.Errorf("carrier %q does not match tier %s", s.Carrier, s.Tier)
	}
	if s.IssuedAt.IsZero() || s.Version < 1 {
		return errors.New("issuedAt and a version of at least 1 are required")
	}
	switch s.Status {
	case StatusIssued:
		if s.VoidReason != "" || !s.VoidedAt.IsZero() {
			return errors.New("an ISSUED label has no void reason or voidedAt")
		}
	case StatusVoided:
		if s.VoidedAt.IsZero() || !shared.ValidTextN(s.VoidReason, MaxReason) || s.Version < 2 {
			return errors.New("a VOIDED label needs a reason, voidedAt and version 2 or more")
		}
	default:
		return fmt.Errorf("status %q is not ISSUED or VOIDED", s.Status)
	}
	return nil
}

// Ref returns the labelRef.
func (l *ShippingLabel) Ref() Ref { return l.ref }

// TrackingNumber returns the tracking number.
func (l *ShippingLabel) TrackingNumber() shared.TrackingNumber { return l.tracking }

// PONumber returns the purchase order number.
func (l *ShippingLabel) PONumber() string { return l.poNumber }

// PackageNo returns the package number within the purchase order.
func (l *ShippingLabel) PackageNo() int { return l.packageNo }

// AssignmentID returns the fulfillment assignment id.
func (l *ShippingLabel) AssignmentID() string { return l.assignmentID }

// OrderID returns the customer order id.
func (l *ShippingLabel) OrderID() string { return l.orderID }

// Tier returns the service tier.
func (l *ShippingLabel) Tier() shared.Tier { return l.tier }

// Carrier returns the carrier chosen at issue time.
func (l *ShippingLabel) Carrier() string { return l.carrier }

// StopKey returns the opaque keyed hash of the normalized address.
func (l *ShippingLabel) StopKey() string { return l.stopKey }

// DestinationPostalPrefix returns the postal prefix of the destination.
func (l *ShippingLabel) DestinationPostalPrefix() string { return l.postalPrefix }

// Status returns ISSUED or VOIDED.
func (l *ShippingLabel) Status() Status { return l.status }

// IssuedAt returns when the label was issued.
func (l *ShippingLabel) IssuedAt() time.Time { return l.issuedAt }

// VoidReason returns why the label was voided (empty while ISSUED).
func (l *ShippingLabel) VoidReason() string { return l.voidReason }

// VoidedAt returns when the label was voided (zero while ISSUED).
func (l *ShippingLabel) VoidedAt() time.Time { return l.voidedAt }

// Version returns the aggregate version.
func (l *ShippingLabel) Version() int64 { return l.version }

// Snapshot returns the persisted form.
func (l *ShippingLabel) Snapshot() Snapshot {
	return Snapshot{
		Ref: l.ref, TrackingNumber: l.tracking, PONumber: l.poNumber, PackageNo: l.packageNo,
		AssignmentID: l.assignmentID, OrderID: l.orderID, Tier: l.tier, Carrier: l.carrier, StopKey: l.stopKey,
		DestinationPostalPrefix: l.postalPrefix, Status: l.status, IssuedAt: l.issuedAt, VoidReason: l.voidReason,
		VoidedAt: l.voidedAt, Version: l.version,
	}
}
