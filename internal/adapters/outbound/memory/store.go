// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so a
// loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate, the unique indexes raise the domain's
// ErrLabelAlreadyIssued and ErrTrackingNumberTaken.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store for
// the whole unit and restores the state taken at the start when the unit fails
// or panics, outbox and inbox included.
package memory

import (
	"context"
	"sync"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu sync.Mutex
	st state
}

// state is everything a unit of work can change.
type state struct {
	labels    map[label.Ref]label.Snapshot
	snapshots map[label.Ref]repository.SealedShipTo
	journeys  map[shared.TrackingNumber]journey.Snapshot
	catalog   map[string]repository.CatalogDimensions
	processed map[string]struct{}
	outbox    []shared.Event
}

func newState() state {
	return state{
		labels:    map[label.Ref]label.Snapshot{},
		snapshots: map[label.Ref]repository.SealedShipTo{},
		journeys:  map[shared.TrackingNumber]journey.Snapshot{},
		catalog:   map[string]repository.CatalogDimensions{},
		processed: map[string]struct{}{},
	}
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{st: newState()} }

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// capture copies the state. Stored snapshots are replaced, never edited in
// place (a journey snapshot's scans slice is copied on Save), so copying the
// maps is enough.
func (s *Store) capture() state {
	return state{
		labels: cloneMap(s.st.labels), snapshots: cloneMap(s.st.snapshots), journeys: cloneMap(s.st.journeys),
		catalog: cloneMap(s.st.catalog), processed: cloneMap(s.st.processed),
		outbox: append([]shared.Event(nil), s.st.outbox...),
	}
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Labels returns the LabelRepository double.
func (s *Store) Labels() *LabelRepository { return &LabelRepository{s} }

// Snapshots returns the ShipToSnapshots double.
func (s *Store) Snapshots() *ShipToSnapshots { return &ShipToSnapshots{s} }

// Journeys returns the JourneyRepository double.
func (s *Store) Journeys() *JourneyRepository { return &JourneyRepository{s} }

// Catalog returns the CatalogDimensionsView double.
func (s *Store) Catalog() *CatalogDimensionsView { return &CatalogDimensionsView{s} }

// Inbox returns the ProcessedEvents double.
func (s *Store) Inbox() *ProcessedEvents { return &ProcessedEvents{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	Labels, Snapshots, Journeys, Scans, Catalog, Processed, OutboxEvents int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate versions,
// so a test can prove that a failed use case stored and changed nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Counts{
		Labels: len(s.st.labels), Snapshots: len(s.st.snapshots), Journeys: len(s.st.journeys),
		Catalog: len(s.st.catalog), Processed: len(s.st.processed), OutboxEvents: len(s.st.outbox),
	}
	for _, l := range s.st.labels {
		c.VersionSum += l.Version
	}
	for _, j := range s.st.journeys {
		c.VersionSum += j.Version
		c.Scans += len(j.Scans)
	}
	return c
}
