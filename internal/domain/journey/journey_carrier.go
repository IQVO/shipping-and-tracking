package journey

import (
	"sort"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// CarrierOption is one declared carrier option of a CARRIER_HANDOFF service
// area (node-topology ADR 0016), as this context's area read model stores it.
type CarrierOption struct {
	CarrierID           string
	HandoffNodeID       string
	CostPerPackageCents int64
	TransitMinutes      int
}

// ChooseCarrier is the deterministic carrier choice of ADR 0015: sort the
// area's options by (costPerPackageCents ascending, transitMinutes descending,
// carrierId ascending) and take the first. The SAME rule is mirrored verbatim
// in delivery-promise (its ADR 0026); both sides compute it from the same area
// assignment version, so the carrier a promise was quoted against is the
// carrier chosen at handoff for an unchanged area. An empty list has no
// choice.
func ChooseCarrier(options []CarrierOption) (CarrierOption, bool) {
	if len(options) == 0 {
		return CarrierOption{}, false
	}
	ordered := append([]CarrierOption(nil), options...)
	sort.SliceStable(ordered, func(a, b int) bool {
		x, y := ordered[a], ordered[b]
		if x.CostPerPackageCents != y.CostPerPackageCents {
			return x.CostPerPackageCents < y.CostPerPackageCents
		}
		if x.TransitMinutes != y.TransitMinutes {
			return x.TransitMinutes > y.TransitMinutes
		}
		return x.CarrierID < y.CarrierID
	})
	return ordered[0], true
}

// checkCarrierChoice validates the three carrier columns together (ADR 0015:
// all null or all set).
func checkCarrierChoice(carrierID, handoffNodeID string, areaVersion int64) error {
	set := carrierID != "" || handoffNodeID != "" || areaVersion != 0
	if !set {
		return nil
	}
	if carrierID == "" || handoffNodeID == "" || areaVersion == 0 {
		return shared.Corrupt("chosenCarrierId, handoffNodeId and areaVersion are all empty or all set")
	}
	if !shared.ValidID(carrierID) || !shared.ValidID(handoffNodeID) {
		return shared.Corrupt("chosenCarrierId %q and handoffNodeId %q are not valid ids", carrierID, handoffNodeID)
	}
	return nil
}

// NoteCarrierChoice records the deterministic choice computed for the journey's
// label prefix at the station it just arrived at (ADR 0015). A later station
// arrival recomputes it from the then-current area view. It rides the SAME
// aggregate version as the station-arrival scan it follows (the scan already
// bumped the version for the save) and raises no event: the
// PackageHandedToCarrier event comes with the handoff scan.
func (j *Journey) NoteCarrierChoice(carrierID, handoffNodeID string, areaVersion int64) error {
	if err := checkCarrierChoice(carrierID, handoffNodeID, areaVersion); err != nil {
		return err
	}
	j.chosenCarrierID, j.handoffNodeID, j.areaVersion = carrierID, handoffNodeID, areaVersion
	return nil
}

// CarrierChosen reports whether the journey carries a carrier choice: a
// package of a CARRIER_HANDOFF area that reached its station.
func (j *Journey) CarrierChosen() bool { return j.chosenCarrierID != "" }

// ChosenCarrierID returns the chosen carrier (empty when none).
func (j *Journey) ChosenCarrierID() string { return j.chosenCarrierID }

// HandoffNodeID returns the handoff node of the chosen carrier (empty when none).
func (j *Journey) HandoffNodeID() string { return j.handoffNodeID }

// AreaVersion returns the area assignment version the choice was computed
// from (zero when none).
func (j *Journey) AreaVersion() int64 { return j.areaVersion }
