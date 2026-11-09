package report

import (
	"errors"
	"fmt"
	"slices"
)

// The KPI rules of ADR 0012. They are functions, not package variables, so the
// scan sets cannot be mutated by a caller and mutation testing reaches them.

// TouchScanTypes is the scan vocabulary that counts as a touch: an applied scan
// that is a physical handling of the package before its delivery. Not touches:
// SHIPMENT_CONFIRMED (the vendor's confirmation, the package has not moved) and
// DELIVERED (the outcome itself).
func TouchScanTypes() []string {
	return []string{"PICKUP_DEPARTURE", "DEPARTURE", "TRANSIT_ARRIVAL", "STATION_ARRIVAL", "OUT_FOR_DELIVERY", "DELIVERY_FAILED"}
}

// FailedAttemptScanTypes is the scan vocabulary of a failed delivery attempt.
// A delivered package's attempts are these scans plus its DELIVERED scan.
func FailedAttemptScanTypes() []string { return []string{"DELIVERY_FAILED"} }

// IsTouch reports whether scanType is in TouchScanTypes.
func IsTouch(scanType string) bool { return slices.Contains(TouchScanTypes(), scanType) }

// IsFailedAttempt reports whether scanType is in FailedAttemptScanTypes.
func IsFailedAttempt(scanType string) bool {
	return slices.Contains(FailedAttemptScanTypes(), scanType)
}

// TouchesP50 and TouchesP90 are the continuous-percentile ranks of the
// touches-per-package distribution.
func TouchesP50() float64 { return 0.5 }

// TouchesP90 is the 90th-percentile rank.
func TouchesP90() float64 { return 0.9 }

// UnknownTier is the tier a delivered package reports under while its
// LabelIssued has not been projected.
func UnknownTier() string { return "UNKNOWN" }

// OutcomeOf is the terminal outcome a Kind records, and false for the kinds
// that record none.
func OutcomeOf(k Kind) (string, bool) {
	switch k {
	case KindPackageDelivered:
		return OutcomeDelivered, true
	case KindPackageReturned:
		return OutcomeReturned, true
	case KindPackageLost:
		return OutcomeLost, true
	default:
		return "", false
	}
}

// ErrInvalidEvent marks a fact that can never be projected: a required field is
// missing. It is deterministic, so the projector dead-letters it.
var ErrInvalidEvent = errors.New("report: invalid analytics event")

// Validate checks the fields every fact and the fields its Kind needs.
func (e Event) Validate() error {
	missing := firstMissing(
		required{"id", e.EventID != ""},
		required{"time", !e.At.IsZero()},
		required{"trackingNumber", e.TrackingNumber != ""},
		required{"the business instant", !e.OccurredAt.IsZero()},
	)
	if missing == "" {
		missing = e.kindMissing()
	}
	if missing != "" {
		return fmt.Errorf("%w: %s %s: %s is required", ErrInvalidEvent, e.Kind, e.EventID, missing)
	}
	return nil
}

// kindMissing names the first missing field of the Kind's own payload, or
// rejects a Kind the projection does not know.
func (e Event) kindMissing() string {
	if e.Kind == KindLabelIssued {
		return firstMissing(required{"labelRef", e.LabelRef != ""}, required{"tier", e.Tier != ""})
	}
	if e.Kind == KindPackageScanned {
		return firstMissing(required{"scanId", e.ScanID != ""}, required{"scanType", e.ScanType != ""})
	}
	if _, ok := OutcomeOf(e.Kind); ok {
		return ""
	}
	return "a known kind"
}

// required is one field check: name and whether it is present.
type required struct {
	name    string
	present bool
}

// firstMissing names the first absent field, or "" when every one is present.
func firstMissing(fields ...required) string {
	for _, f := range fields {
		if !f.present {
			return f.name
		}
	}
	return ""
}
