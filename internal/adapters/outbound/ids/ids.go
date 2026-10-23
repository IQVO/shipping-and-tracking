// Package ids is the adapter of ports.IDGenerator: a label ref is "lbl-<uuid>"
// and a tracking number is "RN" plus 16 upper-case base-32 characters from a
// crypto random source (ADR 0002 A2). Every id passes shared.ValidID.
package ids

import (
	"crypto/rand"
	"encoding/base32"

	"github.com/google/uuid"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// TrackingPrefix starts every tracking number this service mints.
const TrackingPrefix = "RN"

// trackingRandomBytes is 10 bytes = 80 bits = 16 base-32 characters.
const trackingRandomBytes = 10

// Random mints ids from uuid v4 and crypto/rand.
type Random struct{}

var _ ports.IDGenerator = Random{}

// NewLabelRef implements ports.IDGenerator.
func (Random) NewLabelRef() label.Ref { return label.Ref("lbl-" + uuid.NewString()) }

// NewTrackingNumber implements ports.IDGenerator.
func (Random) NewTrackingNumber() shared.TrackingNumber {
	b := make([]byte, trackingRandomBytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error()) // the OS random source failing is not recoverable
	}
	return shared.TrackingNumber(TrackingPrefix + base32.StdEncoding.EncodeToString(b))
}
