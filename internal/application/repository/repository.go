// Package repository holds the non-interface vocabulary of the ports: the
// values that cross a repository boundary but are not aggregates. It has no
// behaviour; the interfaces are in internal/application/ports.
package repository

import (
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// SealedShipTo is the encrypted ship-to snapshot of one label: AES-256-GCM
// ciphertext with its nonce and the id of the key that sealed it. The service
// stores it and deletes it; it never decrypts it for a response (ADR 0002 A9).
type SealedShipTo struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

// StaleCursor is the keyset position of a ListStale page.
type StaleCursor struct {
	LastScanAt     time.Time
	TrackingNumber string
}

// StaleFilter selects the open journeys the silence sweep may act on: past
// LABELLED, last applied scan before Before, and not already excepted for that
// silence unless they are IN_TRANSIT (which may escalate to LOST). Pages are
// ordered by (last scan, tracking number) ascending; After is exclusive.
type StaleFilter struct {
	Before time.Time
	After  StaleCursor
	Limit  int
}

// CatalogDimensions is one row of the CatalogDimensionsView read model.
type CatalogDimensions struct {
	CatalogItemID string
	Title         string
	LengthMm      int64
	WidthMm       int64
	HeightMm      int64
	WeightG       int64
	Hazmat        bool
	Fragile       bool
}

// DeliveryMode of a service area (ADR 0015): NETWORK_ROUTE is the zero value,
// so an event decoded from a producer that predates C4 means the old behaviour.
type DeliveryMode string

// The two delivery modes of a service area.
const (
	DeliveryModeNetworkRoute   DeliveryMode = "NETWORK_ROUTE"
	DeliveryModeCarrierHandoff DeliveryMode = "CARRIER_HANDOFF"
)

// AreaMode is one row of the AreaModeView read model: node-topology's
// ServiceAreaAssigned folded locally, with the assignment version as the
// staleness guard.
type AreaMode struct {
	PostalPrefix string
	DeliveryMode DeliveryMode
	Carriers     []journey.CarrierOption
	Version      int64
}
