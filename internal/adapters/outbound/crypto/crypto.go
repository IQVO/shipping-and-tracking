// Package crypto holds the two keyed primitives of the ship-to path: the
// AES-256-GCM sealer of the ship-to snapshot (ports.ShipToSealer) and the
// HMAC-SHA-256 keyer of the opaque stopKey (ports.StopKeyer). Both keys come
// from mandatory environment variables (ADR 0002 A9, A10); neither is logged.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// KeyBytes is the AES-256 key length.
const KeyBytes = 32

// MinStopSecretBytes is the shortest accepted stopKey secret.
const MinStopSecretBytes = 16

// ErrBadKey marks a key that is not usable (wrong length or encoding).
var ErrBadKey = errors.New("bad key")

// Sealer encrypts with AES-256-GCM and a fresh random nonce per call.
type Sealer struct {
	aead  cipher.AEAD
	keyID string
}

var _ ports.ShipToSealer = (*Sealer)(nil)

// DecodeKey decodes a base64 (standard or URL, padded or not) AES-256 key.
func DecodeKey(b64 string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := enc.DecodeString(b64); err == nil {
			if len(key) != KeyBytes {
				return nil, fmt.Errorf("%w: want %d bytes, got %d", ErrBadKey, KeyBytes, len(key))
			}
			return key, nil
		}
	}
	return nil, fmt.Errorf("%w: not base64", ErrBadKey)
}

// NewSealer builds a Sealer over a 32-byte key. keyID names the key in the
// stored snapshot (the first 8 hex characters of its SHA-256), so a rotation can
// tell which key sealed which row.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("%w: want %d bytes, got %d", ErrBadKey, KeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &Sealer{aead: aead, keyID: hex.EncodeToString(sum[:4])}, nil
}

// Seal implements ports.ShipToSealer.
func (s *Sealer) Seal(plaintext []byte) (repository.SealedShipTo, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return repository.SealedShipTo{}, fmt.Errorf("nonce: %w", err)
	}
	return repository.SealedShipTo{KeyID: s.keyID, Nonce: nonce, Ciphertext: s.aead.Seal(nil, nonce, plaintext, []byte(s.keyID))}, nil
}

// Open decrypts a snapshot sealed by the same key. The service itself never
// opens a snapshot; the method exists for the tests and for a future restricted
// label-printing route.
func (s *Sealer) Open(sealed repository.SealedShipTo) ([]byte, error) {
	if sealed.KeyID != s.keyID {
		return nil, fmt.Errorf("%w: snapshot sealed by key %s, this is %s", ErrBadKey, sealed.KeyID, s.keyID)
	}
	return s.aead.Open(nil, sealed.Nonce, sealed.Ciphertext, []byte(sealed.KeyID))
}

// KeyID is the id this sealer writes into snapshots.
func (s *Sealer) KeyID() string { return s.keyID }

// StopKeyer derives the stopKey: hex HMAC-SHA-256 of the normalized address.
type StopKeyer struct{ secret []byte }

var _ ports.StopKeyer = (*StopKeyer)(nil)

// NewStopKeyer builds a StopKeyer over a secret of at least MinStopSecretBytes.
func NewStopKeyer(secret []byte) (*StopKeyer, error) {
	if len(secret) < MinStopSecretBytes {
		return nil, fmt.Errorf("%w: the stopKey secret needs at least %d bytes", ErrBadKey, MinStopSecretBytes)
	}
	return &StopKeyer{secret: append([]byte(nil), secret...)}, nil
}

// StopKey implements ports.StopKeyer.
func (k *StopKeyer) StopKey(normalized string) string {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}
