package postgres

import (
	"context"
	"errors"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PickupPointRepo is the Postgres twin of the PickupPointView read model
// (ADR 0016): one row per pickup point node, retired points deleted.
type PickupPointRepo struct{ pool *pgxpool.Pool }

// NewPickupPointRepo constructs the repo over pool.
func NewPickupPointRepo(pool *pgxpool.Pool) *PickupPointRepo { return &PickupPointRepo{pool} }

var _ ports.PickupPointView = (*PickupPointRepo)(nil)

// Upsert implements ports.PickupPointView.
func (r *PickupPointRepo) Upsert(ctx context.Context, p repository.PickupPoint) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO pickup_points (id, station_node_id, postal_prefix, state)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET station_node_id = EXCLUDED.station_node_id,
			postal_prefix = EXCLUDED.postal_prefix, state = EXCLUDED.state, updated_at = now()`,
		p.ID, p.StationNodeID, p.PostalPrefix, p.State)
	return err
}

// Get implements ports.PickupPointView.
func (r *PickupPointRepo) Get(ctx context.Context, id string) (repository.PickupPoint, error) {
	var p repository.PickupPoint
	err := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT id, station_node_id, postal_prefix, state FROM pickup_points WHERE id = $1`, id).
		Scan(&p.ID, &p.StationNodeID, &p.PostalPrefix, &p.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.PickupPoint{}, ports.ErrNotFound
	}
	return p, err
}

// Delete implements ports.PickupPointView.
func (r *PickupPointRepo) Delete(ctx context.Context, id string) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `DELETE FROM pickup_points WHERE id = $1`, id)
	return err
}
