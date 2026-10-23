// Package usecases holds the application's use cases, one per command or query
// of the model and per operation of apis/openapi.yaml. Every command follows one
// shape inside ONE ports.UnitOfWork: load the aggregates, call the domain, save
// them and append the raised events to the outbox, then return a view. Domain,
// shared and port errors pass through unchanged (errors.Is keeps working): the
// HTTP adapter maps them to problem types.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Deps bundles the ports the use cases need.
type Deps struct {
	Labels    ports.LabelRepository
	Snapshots ports.ShipToSnapshots
	Journeys  ports.JourneyRepository
	Catalog   ports.CatalogDimensionsView
	Inbox     ports.ProcessedEvents
	Outbox    ports.EventOutbox
	UoW       ports.UnitOfWork
	Clock     ports.Clock
	IDs       ports.IDGenerator
	ShipTo    ports.ShipToGateway
	Sealer    ports.ShipToSealer
	StopKeys  ports.StopKeyer
}

// writer is what every command embeds.
type writer struct{ Deps }

// now returns the clock's current time in UTC.
func (w writer) now() time.Time { return w.Clock.Now().UTC() }

// publish appends the raised events to the outbox, on the ctx of the unit of work.
func (w writer) publish(ctx context.Context, events []shared.Event) error {
	if err := w.Outbox.Append(ctx, events...); err != nil {
		return fmt.Errorf("append events to outbox: %w", err)
	}
	return nil
}

// maxAttempts bounds how often a unit is rerun after ErrConcurrentUpdate
// (ADR 0002 A15).
const maxAttempts = 3

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// (load, domain call, save, outbox) runs again on fresh state, up to
// maxAttempts times, before the conflict is reported.
func transact[T any](ctx context.Context, uow ports.UnitOfWork, attempt func(ctx context.Context) (T, error)) (T, error) {
	var out T
	run := func(ctx context.Context) error {
		v, err := attempt(ctx)
		if err != nil {
			return err
		}
		out = v
		return nil
	}
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = uow.Do(ctx, run)
		if !errors.Is(err, ports.ErrConcurrentUpdate) {
			break
		}
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// found reports whether a lookup hit: a nil error is a hit, ports.ErrNotFound
// a miss, anything else a failure.
func found(err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ports.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}
