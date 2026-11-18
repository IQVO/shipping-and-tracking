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

// PickupPointRepo is the Postgres twin of the PickupPointView read model
// (ADR 0016): one row per pickup point node. A retired point keeps its row,
// in state RETIRED.
type PickupPointRepo struct{ pool *pgxpool.Pool }

// NewPickupPointRepo constructs the repo over pool.
func NewPickupPointRepo(pool *pgxpool.Pool) *PickupPointRepo { return &PickupPointRepo{pool} }

var _ ports.PickupPointView = (*PickupPointRepo)(nil)

// Register implements ports.PickupPointView. The conflict branch writes the
// descriptive columns only: the state of a point already held is never
// touched by a registration.
func (r *PickupPointRepo) Register(ctx context.Context, p repository.PickupPoint) error {
	if p.ID == "" {
		return fmt.Errorf("pickup point register: blank id")
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO pickup_points (id, station_node_id, postal_prefix, state)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET station_node_id = EXCLUDED.station_node_id,
			postal_prefix = EXCLUDED.postal_prefix, updated_at = now()`,
		p.ID, p.StationNodeID, p.PostalPrefix, p.State)
	return err
}

// SetState implements ports.PickupPointView: an UPDATE of the state column of
// an existing row, never an insert, and never out of RETIRED.
func (r *PickupPointRepo) SetState(ctx context.Context, id, state string) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx,
		`UPDATE pickup_points SET state = $2, updated_at = now() WHERE id = $1 AND state <> $3`,
		id, state, repository.PickupPointRetired)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
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
