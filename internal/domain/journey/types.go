package journey

import (
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// State is the position of a package on its journey.
type State string

// The twelve journey states: the model's eight plus VOIDED (ADR 0002 A13),
// WITH_CARRIER (ADR 0015) and the pickup pair (ADR 0016).
const (
	StateLabelled           State = "LABELLED"
	StateHandedOver         State = "HANDED_OVER"
	StateInTransit          State = "IN_TRANSIT"
	StateAtStation          State = "AT_STATION"
	StateWithCarrier        State = "WITH_CARRIER"
	StateOutForDelivery     State = "OUT_FOR_DELIVERY"
	StateReadyForCollection State = "READY_FOR_COLLECTION"
	StateDelivered          State = "DELIVERED"
	StateCollected          State = "COLLECTED"
	StateReturned           State = "RETURNED"
	StateLost               State = "LOST"
	StateVoided             State = "VOIDED"
)

// Valid reports whether s is one of the twelve states.
func (s State) Valid() bool {
	switch s {
	case StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateWithCarrier,
		StateOutForDelivery, StateReadyForCollection, StateDelivered, StateCollected,
		StateReturned, StateLost, StateVoided:
		return true
	}
	return false
}

// Terminal reports whether no command may change a journey in state s.
func (s State) Terminal() bool {
	switch s {
	case StateDelivered, StateCollected, StateReturned, StateLost, StateVoided:
		return true
	}
	return false
}

// ScanType is the kind of observation a scan records (ADR 0002 A5).
type ScanType string

// The nine scan types.
const (
	ScanShipmentConfirmed ScanType = "SHIPMENT_CONFIRMED"
	ScanPickupDeparture   ScanType = "PICKUP_DEPARTURE"
	ScanDeparture         ScanType = "DEPARTURE"
	ScanTransitArrival    ScanType = "TRANSIT_ARRIVAL"
	ScanStationArrival    ScanType = "STATION_ARRIVAL"
	ScanCarrierHandoff    ScanType = "CARRIER_HANDOFF"
	ScanOutForDelivery    ScanType = "OUT_FOR_DELIVERY"
	ScanDelivered         ScanType = "DELIVERED"
	ScanDeliveryFailed    ScanType = "DELIVERY_FAILED"
	// The pickup pair of ADR 0016: the wave deposits the package at the
	// point; the shopper collects it.
	ScanPointDeposited ScanType = "POINT_DEPOSITED"
	ScanCollected      ScanType = "COLLECTED"
)

// AllScanTypes lists the nine scan types in a stable order.
func AllScanTypes() []ScanType {
	return []ScanType{
		ScanShipmentConfirmed, ScanPickupDeparture, ScanDeparture, ScanTransitArrival,
		ScanStationArrival, ScanCarrierHandoff, ScanOutForDelivery, ScanDelivered, ScanDeliveryFailed,
		ScanPointDeposited, ScanCollected,
	}
}

// Valid reports whether t is one of the nine scan types.
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
// plus HANDED_OVER -> AT_STATION (direct injection). carrier tells whether the
// journey carries a carrier choice (ADR 0015): a failed delivery then returns
// the package to the carrier's custody instead of the station's.
func next(t ScanType, from State, carrier bool) (State, bool) {
	switch t {
	case ScanShipmentConfirmed:
		return StateLabelled, from == StateLabelled
	case ScanPickupDeparture:
		return StateHandedOver, from == StateLabelled
	case ScanDeparture, ScanTransitArrival:
		return StateInTransit, from == StateHandedOver || from == StateInTransit
	case ScanStationArrival:
		return StateAtStation, from == StateHandedOver || from == StateInTransit
	case ScanCarrierHandoff:
		return StateWithCarrier, from == StateAtStation
	case ScanOutForDelivery:
		return StateOutForDelivery, from == StateAtStation || from == StateWithCarrier
	case ScanPointDeposited:
		// The wave's DELIVERED attempt at a point deposits the package
		// (ADR 0016); a FAILED attempt keeps the package on the route back
		// to the station through ScanDeliveryFailed.
		return StateReadyForCollection, from == StateOutForDelivery
	case ScanCollected:
		return StateCollected, from == StateReadyForCollection
	case ScanDelivered:
		return StateDelivered, from == StateOutForDelivery
	case ScanDeliveryFailed:
		if carrier {
			return StateWithCarrier, from == StateOutForDelivery
		}
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
	case StateHandedOver, StateInTransit, StateAtStation, StateWithCarrier:
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
