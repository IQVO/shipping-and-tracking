package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting lbl-1, lbl-2, ... and
// RN000000000000001, RN000000000000002, ...
type SequentialIDs struct {
	mu               sync.Mutex
	labels, trackers int
	// Trackers, when set, is consumed first by NewTrackingNumber (to force a
	// tracking-number collision in a test).
	Trackers []shared.TrackingNumber
}

// NewLabelRef returns the next label ref.
func (g *SequentialIDs) NewLabelRef() label.Ref {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.labels++
	return label.Ref(fmt.Sprintf("lbl-%d", g.labels))
}

// NewTrackingNumber returns the next tracking number.
func (g *SequentialIDs) NewTrackingNumber() shared.TrackingNumber {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.Trackers) > 0 {
		tn := g.Trackers[0]
		g.Trackers = g.Trackers[1:]
		return tn
	}
	g.trackers++
	return shared.TrackingNumber(fmt.Sprintf("RN%015d", g.trackers))
}
