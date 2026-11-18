package label

import "github.com/claudioed/shipping-and-tracking/internal/domain/shared"

// The input checks of Issue, moved out of label.go unchanged to keep each
// production file under the length limit.

func checkNew(in NewLabel) error {
	if !shared.ValidID(string(in.Ref)) {
		return shared.Invalid("labelRef %q is not a valid id", in.Ref)
	}
	if !in.TrackingNumber.Valid() {
		return shared.Invalid("trackingNumber %q is not a valid id", in.TrackingNumber)
	}
	if in.Purpose == "" {
		in.Purpose = PurposeOutbound
	}
	if !in.Purpose.Valid() {
		return shared.Invalid("purpose %q is not OUTBOUND or RETURN", in.Purpose)
	}
	if !shared.ValidID(in.OrderID) {
		return shared.Invalid("orderId %q is not a valid id", in.OrderID)
	}
	if err := checkIdentity(in); err != nil {
		return err
	}
	if in.PackageNo < 1 || in.PackageNo > MaxPackageNo {
		return shared.Invalid("packageNo must be between 1 and %d", MaxPackageNo)
	}
	if !in.Tier.Valid() {
		return shared.Invalid("tier %q is not SAME_DAY, NEXT_DAY, TWO_DAY or STANDARD", in.Tier)
	}
	if in.DestinationStationNodeID != "" && !shared.ValidID(in.DestinationStationNodeID) {
		return shared.Invalid("destinationStationNodeId %q is not a valid id", in.DestinationStationNodeID)
	}
	return checkDerived(in.StopKey, in.DestinationPostalPrefix)
}

// checkIdentity validates the purpose-shaped identity of a label: an outbound
// label hangs off (poNumber, packageNo) and a fulfillment assignment, a
// reverse label off its return authorization alone (ADR 0017).
func checkIdentity(in NewLabel) error {
	if in.Purpose == PurposeReturn {
		return checkReturnIdentity(in)
	}
	for _, f := range [][2]string{{"poNumber", in.PONumber}, {"assignmentId", in.AssignmentID}} {
		if !shared.ValidID(f[1]) {
			return shared.Invalid("%s %q is not a valid id", f[0], f[1])
		}
	}
	if in.ReturnAuthorizationID != "" {
		return shared.Invalid("an OUTBOUND label carries no returnAuthorizationId")
	}
	return nil
}

// checkReturnIdentity: a reverse label names its return authorization and
// carries neither a purchase order nor an assignment.
func checkReturnIdentity(in NewLabel) error {
	if !shared.ValidID(in.ReturnAuthorizationID) {
		return shared.Invalid("returnAuthorizationId %q is not a valid id", in.ReturnAuthorizationID)
	}
	if in.PONumber != "" || in.AssignmentID != "" {
		return shared.Invalid("a RETURN label carries no poNumber or assignmentId")
	}
	return nil
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
