package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// carrierOptionJSON is the JSONB shape of one declared carrier option (the
// producer's wire shape, ADR 0015).
type carrierOptionJSON struct {
	CarrierID           string `json:"carrierId"`
	HandoffNodeID       string `json:"handoffNodeId"`
	CostPerPackageCents int64  `json:"costPerPackageCents"`
	TransitMinutes      int    `json:"transitMinutes"`
}

func optionsToJSON(opts []journey.CarrierOption) ([]byte, error) {
	if len(opts) == 0 {
		return []byte("[]"), nil
	}
	out := make([]carrierOptionJSON, len(opts))
	for i, o := range opts {
		out[i] = carrierOptionJSON{CarrierID: o.CarrierID, HandoffNodeID: o.HandoffNodeID,
			CostPerPackageCents: o.CostPerPackageCents, TransitMinutes: o.TransitMinutes}
	}
	return json.Marshal(out)
}

func optionsFromJSON(raw []byte) ([]journey.CarrierOption, error) {
	var in []carrierOptionJSON
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("decode carrier_options: %w", err)
	}
	out := make([]journey.CarrierOption, len(in))
	for i, o := range in {
		out[i] = journey.CarrierOption{CarrierID: o.CarrierID, HandoffNodeID: o.HandoffNodeID,
			CostPerPackageCents: o.CostPerPackageCents, TransitMinutes: o.TransitMinutes}
	}
	return out, nil
}

// AreaModeRepo is the pgx-backed ports.AreaModeView.
type AreaModeRepo struct{ pool *pgxpool.Pool }

// NewAreaModeRepo constructs an AreaModeRepo over pool.
func NewAreaModeRepo(pool *pgxpool.Pool) *AreaModeRepo { return &AreaModeRepo{pool} }

var _ ports.AreaModeView = (*AreaModeRepo)(nil)

// Apply implements ports.AreaModeView: the assignment-version guard keeps an
// older event from overwriting a newer row.
func (r *AreaModeRepo) Apply(ctx context.Context, a repository.AreaMode) (bool, error) {
	opts, err := optionsToJSON(a.Carriers)
	if err != nil {
		return false, err
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO area_modes (postal_prefix, delivery_mode, carrier_options, assignment_version)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (postal_prefix) DO UPDATE SET delivery_mode = EXCLUDED.delivery_mode,
			carrier_options = EXCLUDED.carrier_options, assignment_version = EXCLUDED.assignment_version,
			updated_at = now()
		WHERE EXCLUDED.assignment_version > area_modes.assignment_version`,
		a.PostalPrefix, string(a.DeliveryMode), opts, a.Version)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Get implements ports.AreaModeView.
func (r *AreaModeRepo) Get(ctx context.Context, postalPrefix string) (repository.AreaMode, error) {
	var (
		mode    string
		opts    []byte
		version int64
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT delivery_mode, carrier_options, assignment_version FROM area_modes WHERE postal_prefix = $1`, postalPrefix).
		Scan(&mode, &opts, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.AreaMode{}, fmt.Errorf("%w: service area %s", ports.ErrNotFound, postalPrefix)
	}
	if err != nil {
		return repository.AreaMode{}, err
	}
	carriers, err := optionsFromJSON(opts)
	if err != nil {
		return repository.AreaMode{}, err
	}
	return repository.AreaMode{PostalPrefix: postalPrefix, DeliveryMode: repository.DeliveryMode(mode),
		Carriers: carriers, Version: version}, nil
}
