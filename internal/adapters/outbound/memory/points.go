package memory

import (
	"context"
	"fmt"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// PickupPointView is the in-memory twin of the PickupPointView read model
// (ADR 0016).
type PickupPointView struct{ s *Store }

// Points returns the PickupPointView double.
func (s *Store) Points() *PickupPointView { return &PickupPointView{s} }

// Upsert implements ports.PickupPointView.
func (r *PickupPointView) Upsert(ctx context.Context, p repository.PickupPoint) error {
	defer r.s.enter(ctx)()
	if p.ID == "" {
		return fmt.Errorf("pickup point upsert: blank id")
	}
	r.s.st.points[p.ID] = p
	return nil
}

// Get implements ports.PickupPointView.
func (r *PickupPointView) Get(ctx context.Context, id string) (repository.PickupPoint, error) {
	defer r.s.enter(ctx)()
	p, ok := r.s.st.points[id]
	if !ok {
		return repository.PickupPoint{}, fmt.Errorf("%w: pickup point %s", ports.ErrNotFound, id)
	}
	return p, nil
}

// Delete implements ports.PickupPointView.
func (r *PickupPointView) Delete(ctx context.Context, id string) error {
	defer r.s.enter(ctx)()
	delete(r.s.st.points, id)
	return nil
}
