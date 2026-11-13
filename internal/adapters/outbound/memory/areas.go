package memory

import (
	"context"
	"fmt"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// AreaModeView is the in-memory ports.AreaModeView.
type AreaModeView struct{ s *Store }

// Apply implements ports.AreaModeView: an assignment version strictly newer
// than the stored one replaces the row; an equal or older one is dropped.
func (r *AreaModeView) Apply(ctx context.Context, a repository.AreaMode) (bool, error) {
	defer r.s.enter(ctx)()
	if stored, ok := r.s.st.areas[a.PostalPrefix]; ok && a.Version <= stored.Version {
		return false, nil
	}
	r.s.st.areas[a.PostalPrefix] = a
	return true, nil
}

// Get implements ports.AreaModeView.
func (r *AreaModeView) Get(ctx context.Context, postalPrefix string) (repository.AreaMode, error) {
	defer r.s.enter(ctx)()
	a, ok := r.s.st.areas[postalPrefix]
	if !ok {
		return repository.AreaMode{}, fmt.Errorf("%w: service area %s", ports.ErrNotFound, postalPrefix)
	}
	return a, nil
}
