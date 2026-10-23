package memory

import (
	"context"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn (aggregates, snapshots, inbox and outbox) is restored; the
// error is returned unchanged. A Do inside a Do joins it.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		return fn(ctx)
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.capture()
	committed := false
	defer func() {
		if !committed {
			u.s.st = before
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.st.outbox = append(o.s.st.outbox, events...)
	return nil
}

// Events returns the appended events in order (a copy of the slice).
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return append([]shared.Event(nil), o.s.st.outbox...)
}

// ProcessedEvents is the in-memory ports.ProcessedEvents.
type ProcessedEvents struct{ s *Store }

// Claim records (consumer, eventID); false when it was recorded before.
func (p *ProcessedEvents) Claim(ctx context.Context, consumer, eventID string) (bool, error) {
	defer p.s.enter(ctx)()
	key := consumer + "\x00" + eventID
	if _, ok := p.s.st.processed[key]; ok {
		return false, nil
	}
	p.s.st.processed[key] = struct{}{}
	return true, nil
}
