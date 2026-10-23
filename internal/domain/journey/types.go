package journey

import (
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// State is the position of a package on its journey.
type State string

// The nine journey states: the model's eight plus VOIDED (ADR 0002 A13).
const (
	StateLabelled       State = "LABELLED"
	StateHandedOver     State = "HANDED_OVER"
	StateInTransit      State = "IN_TRANSIT"
	StateAtStation      State = "AT_STATION"
	StateOutForDelivery State = "OUT_FOR_DELIVERY"
	StateDelivered      State = "DELIVERED"
	StateReturned       State = "RETURNED"
	StateLost           State = "LOST"
	StateVoided         State = "VOIDED"
)

// Valid reports whether s is one of the nine states.
func (s State) Valid() bool {
	switch s {
	case StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery,
		StateDelivered, StateReturned, StateLost, StateVoided:
		return true
	}
	return false
}

// Terminal reports whether no command may change a journey in state s.
func (s State) Terminal() bool {
	switch s {
	case StateDelivered, StateReturned, StateLost, StateVoided:
		return true
	}
	return false
}

// ScanType is the kind of observation a scan records (ADR 0002 A5).
type ScanType string

// The eight scan types.
const (
	ScanShipmentConfirmed ScanType = "SHIPMENT_CONFIRMED"
	ScanPickupDeparture   ScanType = "PICKUP_DEPARTURE"
	ScanDeparture         ScanType = "DEPARTURE"
	ScanTransitArrival    ScanType = "TRANSIT_ARRIVAL"
	ScanStationArrival    ScanType = "STATION_ARRIVAL"
	ScanOutForDelivery    ScanType = "OUT_FOR_DELIVERY"
	ScanDelivered         ScanType = "DELIVERED"
	ScanDeliveryFailed    ScanType = "DELIVERY_FAILED"
)

// AllScanTypes lists the eight scan types in a stable order.
func AllScanTypes() []ScanType {
	return []ScanType{
		ScanShipmentConfirmed, ScanPickupDeparture, ScanDeparture, ScanTransitArrival,
		ScanStationArrival, ScanOutForDelivery, ScanDelivered, ScanDeliveryFailed,
	}
}

// Valid reports whether t is one of the eight scan types.
func (t ScanType) Valid() bool {
	for _, v := range AllScanTypes() {
		if t == v {
			return true
		}
	}
	return false
}

// next returns the state a scan of type t moves a journey in state from to,
// and false when the model has no such edge. This is the single table of
// RecordScan edges (ADR 0002 A5): the model's seven RecordScan transitions
// plus HANDED_OVER -> AT_STATION (direct injection).
func next(t ScanType, from State) (State, bool) {
	switch t {
	case ScanShipmentConfirmed:
		return StateLabelled, from == StateLabelled
	case ScanPickupDeparture:
		return StateHandedOver, from == StateLabelled
	case ScanDeparture, ScanTransitArrival:
		return StateInTransit, from == StateHandedOver || from == StateInTransit
	case ScanStationArrival:
		return StateAtStation, from == StateHandedOver || from == StateInTransit
	case ScanOutForDelivery:
		return StateOutForDelivery, from == StateAtStation
	case ScanDelivered:
		return StateDelivered, from == StateOutForDelivery
	case ScanDeliveryFailed:
		return StateAtStation, from == StateOutForDelivery
	}
	return "", false
}

// Silence policy (ADR 0002 A7, A8). The windows are whole hours; the helpers
// below turn them into durations.
const (
	// SameDayWindowHours, NextDayWindowHours, TwoDayWindowHours and
	// StandardWindowHours are the silence tolerated per tier while the package
	// is handed over, in transit or at a station.
	SameDayWindowHours  = 6
	NextDayWindowHours  = 24
	TwoDayWindowHours   = 48
	StandardWindowHours = 72
	// OutForDeliveryWindowHours is the silence tolerated while out for delivery.
	OutForDeliveryWindowHours = 12
	// LostAfterExceptionHours is how long a NO_SCAN exception may stay
	// unanswered on an IN_TRANSIT journey before the package is declared lost.
	LostAfterExceptionHours = 48
)

// ReasonNoScan is the only exception reason.
const ReasonNoScan = "NO_SCAN"

func hours(n int) time.Duration { return time.Duration(n) * time.Hour }

// LostAfterException returns LostAfterExceptionHours as a duration.
func LostAfterException() time.Duration { return hours(LostAfterExceptionHours) }

// MinSilenceWindow is the shortest window of any watched state: a journey
// whose last scan is more recent than this can never be silent.
func MinSilenceWindow() time.Duration { return hours(SameDayWindowHours) }

// SilenceWindow returns how long a journey in the given state and tier may go
// without an applied scan before ExceptionOnSilence raises NO_SCAN. Zero means
// the state is not watched (LABELLED, and the terminal states).
func SilenceWindow(tier shared.Tier, state State) time.Duration {
	switch state {
	case StateOutForDelivery:
		return hours(OutForDeliveryWindowHours)
	case StateHandedOver, StateInTransit, StateAtStation:
		return tierWindow(tier)
	}
	return 0
}

func tierWindow(tier shared.Tier) time.Duration {
	switch tier {
	case shared.TierSameDay:
		return hours(SameDayWindowHours)
	case shared.TierNextDay:
		return hours(NextDayWindowHours)
	case shared.TierTwoDay:
		return hours(TwoDayWindowHours)
	case shared.TierStandard:
		return hours(StandardWindowHours)
	}
	return 0
}
