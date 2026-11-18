package memory

import (
	"context"
	"fmt"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// NodeDestinationView is the in-memory twin of the NodeDestinationView read
// model (ADR 0018).
type NodeDestinationView struct{ s *Store }

var _ ports.NodeDestinationView = (*NodeDestinationView)(nil)

// NodeDestinations returns the NodeDestinationView double.
func (s *Store) NodeDestinations() *NodeDestinationView { return &NodeDestinationView{s} }

// Upsert implements ports.NodeDestinationView.
func (r *NodeDestinationView) Upsert(ctx context.Context, n repository.NodeDestination) error {
	defer r.s.enter(ctx)()
	if n.NodeID == "" {
		return fmt.Errorf("node destination upsert: blank node id")
	}
	r.s.st.nodes[n.NodeID] = n
	return nil
}

// Get implements ports.NodeDestinationView.
func (r *NodeDestinationView) Get(ctx context.Context, nodeID string) (repository.NodeDestination, error) {
	defer r.s.enter(ctx)()
	n, ok := r.s.st.nodes[nodeID]
	if !ok {
		return repository.NodeDestination{}, fmt.Errorf("%w: node %s", ports.ErrNotFound, nodeID)
	}
	return n, nil
}
