package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NodeDestinationRepo is the Postgres twin of the NodeDestinationView read
// model (ADR 0018): one row per node of node-topology, every node type, with
// the destination prefix this context derived for it.
type NodeDestinationRepo struct{ pool *pgxpool.Pool }

// NewNodeDestinationRepo constructs the repo over pool.
func NewNodeDestinationRepo(pool *pgxpool.Pool) *NodeDestinationRepo {
	return &NodeDestinationRepo{pool}
}

var _ ports.NodeDestinationView = (*NodeDestinationRepo)(nil)

// Upsert implements ports.NodeDestinationView.
func (r *NodeDestinationRepo) Upsert(ctx context.Context, n repository.NodeDestination) error {
	if n.NodeID == "" {
		return fmt.Errorf("node destination upsert: blank node id")
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO node_destinations (node_id, postal_prefix)
		VALUES ($1, $2)
		ON CONFLICT (node_id) DO UPDATE SET postal_prefix = EXCLUDED.postal_prefix, updated_at = now()`,
		n.NodeID, n.PostalPrefix)
	return err
}

// Get implements ports.NodeDestinationView.
func (r *NodeDestinationRepo) Get(ctx context.Context, nodeID string) (repository.NodeDestination, error) {
	var n repository.NodeDestination
	err := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT node_id, postal_prefix FROM node_destinations WHERE node_id = $1`, nodeID).
		Scan(&n.NodeID, &n.PostalPrefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.NodeDestination{}, ports.ErrNotFound
	}
	return n, err
}
