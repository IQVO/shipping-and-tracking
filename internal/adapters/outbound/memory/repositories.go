package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// LabelRepository is the in-memory ports.LabelRepository.
type LabelRepository struct{ s *Store }

// Get implements ports.LabelRepository.
func (r *LabelRepository) Get(ctx context.Context, ref label.Ref) (*label.ShippingLabel, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.labels[ref]
	if !ok {
		return nil, fmt.Errorf("%w: label %s", ports.ErrNotFound, ref)
	}
	return label.Restore(snap)
}

// FindByPackage implements ports.LabelRepository.
func (r *LabelRepository) FindByPackage(ctx context.Context, poNumber string, packageNo int) (*label.ShippingLabel, error) {
	defer r.s.enter(ctx)()
	for _, snap := range r.s.st.labels {
		if snap.PONumber == poNumber && snap.PackageNo == packageNo {
			return label.Restore(snap)
		}
	}
	return nil, fmt.Errorf("%w: label of package %d of %s", ports.ErrNotFound, packageNo, poNumber)
}

// Save implements ports.LabelRepository.
func (r *LabelRepository) Save(ctx context.Context, l *label.ShippingLabel) error {
	defer r.s.enter(ctx)()
	snap := l.Snapshot()
	stored, exists := r.s.st.labels[snap.Ref]
	switch {
	case !exists && snap.Version != 1, exists && stored.Version != snap.Version-1:
		return fmt.Errorf("%w: label %s", ports.ErrConcurrentUpdate, snap.Ref)
	case exists:
		r.s.st.labels[snap.Ref] = snap
		return nil
	}
	for _, other := range r.s.st.labels {
		if other.PONumber == snap.PONumber && other.PackageNo == snap.PackageNo {
			return fmt.Errorf("%w: package %d of %s", label.ErrLabelAlreadyIssued, snap.PackageNo, snap.PONumber)
		}
		if other.TrackingNumber == snap.TrackingNumber {
			return fmt.Errorf("%w: %s", label.ErrTrackingNumberTaken, snap.TrackingNumber)
		}
	}
	r.s.st.labels[snap.Ref] = snap
	return nil
}

// ShipToSnapshots is the in-memory ports.ShipToSnapshots.
type ShipToSnapshots struct{ s *Store }

// Put implements ports.ShipToSnapshots.
func (r *ShipToSnapshots) Put(ctx context.Context, ref label.Ref, sealed repository.SealedShipTo) error {
	defer r.s.enter(ctx)()
	if _, ok := r.s.st.snapshots[ref]; ok {
		return fmt.Errorf("%w: ship-to snapshot of %s is written once", ports.ErrConcurrentUpdate, ref)
	}
	r.s.st.snapshots[ref] = repository.SealedShipTo{
		KeyID: sealed.KeyID, Nonce: append([]byte(nil), sealed.Nonce...), Ciphertext: append([]byte(nil), sealed.Ciphertext...),
	}
	return nil
}

// Has implements ports.ShipToSnapshots.
func (r *ShipToSnapshots) Has(ctx context.Context, ref label.Ref) (bool, error) {
	defer r.s.enter(ctx)()
	_, ok := r.s.st.snapshots[ref]
	return ok, nil
}

// PurgeClosedBefore implements ports.ShipToSnapshots.
func (r *ShipToSnapshots) PurgeClosedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	defer r.s.enter(ctx)()
	n := 0
	for _, j := range r.s.st.journeys {
		if j.ClosedAt.IsZero() || !j.ClosedAt.Before(cutoff) {
			continue
		}
		if _, ok := r.s.st.snapshots[label.Ref(j.LabelRef)]; ok {
			delete(r.s.st.snapshots, label.Ref(j.LabelRef))
			n++
		}
	}
	return n, nil
}

// JourneyRepository is the in-memory ports.JourneyRepository.
type JourneyRepository struct{ s *Store }

// Get implements ports.JourneyRepository.
func (r *JourneyRepository) Get(ctx context.Context, tn shared.TrackingNumber) (*journey.Journey, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.journeys[tn]
	if !ok {
		return nil, fmt.Errorf("%w: journey %s", ports.ErrNotFound, tn)
	}
	return journey.Restore(snap)
}

// Save implements ports.JourneyRepository.
func (r *JourneyRepository) Save(ctx context.Context, j *journey.Journey) error {
	defer r.s.enter(ctx)()
	snap := j.Snapshot()
	stored, exists := r.s.st.journeys[snap.TrackingNumber]
	if !exists && snap.Version != 1 || exists && stored.Version != snap.Version-1 {
		return fmt.Errorf("%w: journey %s", ports.ErrConcurrentUpdate, snap.TrackingNumber)
	}
	r.s.st.journeys[snap.TrackingNumber] = snap
	return nil
}

// ListStale implements ports.JourneyRepository.
func (r *JourneyRepository) ListStale(ctx context.Context, f repository.StaleFilter) ([]*journey.Journey, error) {
	defer r.s.enter(ctx)()
	var snaps []journey.Snapshot
	for _, s := range r.s.st.journeys {
		if isCandidate(s, f) {
			snaps = append(snaps, s)
		}
	}
	sort.Slice(snaps, func(a, b int) bool {
		if !snaps[a].LastScanAt.Equal(snaps[b].LastScanAt) {
			return snaps[a].LastScanAt.Before(snaps[b].LastScanAt)
		}
		return snaps[a].TrackingNumber < snaps[b].TrackingNumber
	})
	if f.Limit > 0 && len(snaps) > f.Limit {
		snaps = snaps[:f.Limit]
	}
	out := make([]*journey.Journey, 0, len(snaps))
	for _, s := range snaps {
		j, err := journey.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func isCandidate(s journey.Snapshot, f repository.StaleFilter) bool {
	switch s.State {
	case journey.StateHandedOver, journey.StateInTransit, journey.StateAtStation, journey.StateOutForDelivery:
	default:
		return false
	}
	if !s.LastScanAt.Before(f.Before) {
		return false
	}
	answered := !s.LastExceptionAt.IsZero() && !s.LastExceptionAt.Before(s.LastScanAt)
	if answered && s.State != journey.StateInTransit {
		return false
	}
	if f.After.TrackingNumber == "" && f.After.LastScanAt.IsZero() {
		return true
	}
	if !s.LastScanAt.Equal(f.After.LastScanAt) {
		return s.LastScanAt.After(f.After.LastScanAt)
	}
	return string(s.TrackingNumber) > f.After.TrackingNumber
}

// CatalogDimensionsView is the in-memory ports.CatalogDimensionsView.
type CatalogDimensionsView struct{ s *Store }

// Upsert implements ports.CatalogDimensionsView.
func (r *CatalogDimensionsView) Upsert(ctx context.Context, d repository.CatalogDimensions) error {
	defer r.s.enter(ctx)()
	r.s.st.catalog[d.CatalogItemID] = d
	return nil
}

// Get implements ports.CatalogDimensionsView.
func (r *CatalogDimensionsView) Get(ctx context.Context, id string) (repository.CatalogDimensions, error) {
	defer r.s.enter(ctx)()
	d, ok := r.s.st.catalog[id]
	if !ok {
		return repository.CatalogDimensions{}, fmt.Errorf("%w: catalog item %s", ports.ErrNotFound, id)
	}
	return d, nil
}
