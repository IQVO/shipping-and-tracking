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

var _ ports.PickupPointView = (*PickupPointView)(nil)

// Points returns the PickupPointView double.
func (s *Store) Points() *PickupPointView { return &PickupPointView{s} }

// Register implements ports.PickupPointView: the descriptive data is
// replaced, the state of a point already held is kept.
func (r *PickupPointView) Register(ctx context.Context, p repository.PickupPoint) error {
	defer r.s.enter(ctx)()
	if p.ID == "" {
		return fmt.Errorf("pickup point register: blank id")
	}
	if held, ok := r.s.st.points[p.ID]; ok {
		p.State = held.State
	}
	r.s.st.points[p.ID] = p
	return nil
}

// SetState implements ports.PickupPointView: only the state of an existing,
// not yet RETIRED point changes.
func (r *PickupPointView) SetState(ctx context.Context, id, state string) (bool, error) {
	defer r.s.enter(ctx)()
	p, ok := r.s.st.points[id]
	if !ok || p.State == repository.PickupPointRetired {
		return false, nil
	}
	p.State = state
	r.s.st.points[id] = p
	return true, nil
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
