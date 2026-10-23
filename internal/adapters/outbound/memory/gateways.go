package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
)

// ShipToGateway is a scripted ports.ShipToGateway: orders map to synthetic
// addresses, an unknown order is ports.ErrNotFound, and Fail makes every call
// ports.ErrShipToUnavailable.
type ShipToGateway struct {
	mu     sync.Mutex
	orders map[string]shipto.Address
	fail   bool
	calls  []string
}

// NewShipToGateway returns an empty gateway.
func NewShipToGateway() *ShipToGateway { return &ShipToGateway{orders: map[string]shipto.Address{}} }

// Put scripts the ship-to of an order.
func (g *ShipToGateway) Put(orderID string, a shipto.Address) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.orders[orderID] = a
}

// Fail switches the unavailable mode on or off.
func (g *ShipToGateway) Fail(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail = on
}

// Calls returns the order ids asked for, in order.
func (g *ShipToGateway) Calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

// GetShipTo implements ports.ShipToGateway.
func (g *ShipToGateway) GetShipTo(_ context.Context, orderID string) (shipto.Address, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, orderID)
	if g.fail {
		return shipto.Address{}, fmt.Errorf("%w: scripted failure", ports.ErrShipToUnavailable)
	}
	a, ok := g.orders[orderID]
	if !ok {
		return shipto.Address{}, fmt.Errorf("%w: order %s", ports.ErrNotFound, orderID)
	}
	return a, nil
}

// MarkSealer is a ports.ShipToSealer for tests: it does NOT encrypt. It tags
// the plaintext so a test can see what was sealed; the real AES-GCM sealer is
// in internal/adapters/outbound/crypto.
type MarkSealer struct{ Err error }

// Seal implements ports.ShipToSealer.
func (m MarkSealer) Seal(plaintext []byte) (repository.SealedShipTo, error) {
	if m.Err != nil {
		return repository.SealedShipTo{}, m.Err
	}
	return repository.SealedShipTo{KeyID: "test", Nonce: []byte("nonce"), Ciphertext: append([]byte("sealed:"), plaintext...)}, nil
}

// HashKeyer is a ports.StopKeyer for tests: hex SHA-256 of the normalized text.
type HashKeyer struct{}

// StopKey implements ports.StopKeyer.
func (HashKeyer) StopKey(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
