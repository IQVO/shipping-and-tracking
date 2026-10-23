package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// Constraint names of 0001_schema.up.sql that map to domain errors.
const (
	labelPackageKey  = "shipping_labels_package_key"
	labelTrackingKey = "shipping_labels_tracking_key"
	labelPKey        = "shipping_labels_pkey"
)

// unique reports whether err is a unique violation, and of which constraint.
func unique(err error) (constraint string, ok bool) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return pg.ConstraintName, true
	}
	return "", false
}

// nullTime maps the zero time to NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// orZero maps a NULL timestamp to the zero time.
func orZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// LabelRepo is the pgx-backed ports.LabelRepository.
type LabelRepo struct{ pool *pgxpool.Pool }

// NewLabelRepo constructs a LabelRepo over pool.
func NewLabelRepo(pool *pgxpool.Pool) *LabelRepo { return &LabelRepo{pool: pool} }

var _ ports.LabelRepository = (*LabelRepo)(nil)

const labelColumns = `label_ref, tracking_number, po_number, package_no, assignment_id, order_id, tier, carrier, stop_key,
	destination_postal_prefix, status, issued_at, void_reason, voided_at, version`

func scanLabel(row pgx.Row) (*label.ShippingLabel, error) {
	var (
		s      label.Snapshot
		tier   string
		status string
		reason *string
		voided *time.Time
	)
	if err := row.Scan(&s.Ref, &s.TrackingNumber, &s.PONumber, &s.PackageNo, &s.AssignmentID, &s.OrderID, &tier, &s.Carrier,
		&s.StopKey, &s.DestinationPostalPrefix, &status, &s.IssuedAt, &reason, &voided, &s.Version); err != nil {
		return nil, err
	}
	s.Tier, s.Status, s.VoidReason, s.VoidedAt = tierOf(tier), label.Status(status), deref(reason), orZero(voided)
	s.IssuedAt = s.IssuedAt.UTC()
	return label.Restore(s)
}

// Get implements ports.LabelRepository.
func (r *LabelRepo) Get(ctx context.Context, ref label.Ref) (*label.ShippingLabel, error) {
	l, err := scanLabel(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+labelColumns+` FROM shipping_labels WHERE label_ref = $1`, string(ref)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: label %s", ports.ErrNotFound, ref)
	}
	return l, err
}

// FindByPackage implements ports.LabelRepository.
func (r *LabelRepo) FindByPackage(ctx context.Context, poNumber string, packageNo int) (*label.ShippingLabel, error) {
	l, err := scanLabel(queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT `+labelColumns+` FROM shipping_labels WHERE po_number = $1 AND package_no = $2`, poNumber, packageNo))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: label of package %d of %s", ports.ErrNotFound, packageNo, poNumber)
	}
	return l, err
}

// Save implements ports.LabelRepository.
func (r *LabelRepo) Save(ctx context.Context, l *label.ShippingLabel) error {
	s := l.Snapshot()
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO shipping_labels (`+labelColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			string(s.Ref), string(s.TrackingNumber), s.PONumber, s.PackageNo, s.AssignmentID, s.OrderID, string(s.Tier), s.Carrier,
			s.StopKey, s.DestinationPostalPrefix, string(s.Status), s.IssuedAt, nullString(s.VoidReason), nullTime(s.VoidedAt), s.Version)
		return mapLabelInsertError(err, s)
	}
	tag, err := q.Exec(ctx, `UPDATE shipping_labels SET status = $2, void_reason = $3, voided_at = $4, version = $5
		WHERE label_ref = $1 AND version = $6`,
		string(s.Ref), string(s.Status), nullString(s.VoidReason), nullTime(s.VoidedAt), s.Version, s.Version-1)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: label %s", ports.ErrConcurrentUpdate, s.Ref)
	}
	return nil
}

func mapLabelInsertError(err error, s label.Snapshot) error {
	c, ok := unique(err)
	switch {
	case !ok:
		return err
	case c == labelPackageKey:
		return fmt.Errorf("%w: package %d of %s", label.ErrLabelAlreadyIssued, s.PackageNo, s.PONumber)
	case c == labelTrackingKey:
		return fmt.Errorf("%w: %s", label.ErrTrackingNumberTaken, s.TrackingNumber)
	default: // labelPKey: the same label saved twice
		return fmt.Errorf("%w: label %s", ports.ErrConcurrentUpdate, s.Ref)
	}
}

// SnapshotRepo is the pgx-backed ports.ShipToSnapshots.
type SnapshotRepo struct{ pool *pgxpool.Pool }

// NewSnapshotRepo constructs a SnapshotRepo over pool.
func NewSnapshotRepo(pool *pgxpool.Pool) *SnapshotRepo { return &SnapshotRepo{pool: pool} }

var _ ports.ShipToSnapshots = (*SnapshotRepo)(nil)

// Put implements ports.ShipToSnapshots.
func (r *SnapshotRepo) Put(ctx context.Context, ref label.Ref, sealed repository.SealedShipTo) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx,
		`INSERT INTO ship_to_snapshots (label_ref, key_id, nonce, ciphertext) VALUES ($1, $2, $3, $4)`,
		string(ref), sealed.KeyID, sealed.Nonce, sealed.Ciphertext)
	if _, ok := unique(err); ok {
		return fmt.Errorf("%w: ship-to snapshot of %s is written once", ports.ErrConcurrentUpdate, ref)
	}
	return err
}

// Has implements ports.ShipToSnapshots.
func (r *SnapshotRepo) Has(ctx context.Context, ref label.Ref) (bool, error) {
	var has bool
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ship_to_snapshots WHERE label_ref = $1)`, string(ref)).Scan(&has)
	return has, err
}

// PurgeClosedBefore implements ports.ShipToSnapshots.
func (r *SnapshotRepo) PurgeClosedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		DELETE FROM ship_to_snapshots s USING package_journeys j
		WHERE j.label_ref = s.label_ref AND j.closed_at IS NOT NULL AND j.closed_at < $1`, cutoff.UTC())
	return int(tag.RowsAffected()), err
}

// CatalogRepo is the pgx-backed ports.CatalogDimensionsView.
type CatalogRepo struct{ pool *pgxpool.Pool }

// NewCatalogRepo constructs a CatalogRepo over pool.
func NewCatalogRepo(pool *pgxpool.Pool) *CatalogRepo { return &CatalogRepo{pool: pool} }

var _ ports.CatalogDimensionsView = (*CatalogRepo)(nil)

// Upsert implements ports.CatalogDimensionsView.
func (r *CatalogRepo) Upsert(ctx context.Context, d repository.CatalogDimensions) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO catalog_dimensions (catalog_item_id, title, length_mm, width_mm, height_mm, weight_g, hazmat, fragile)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (catalog_item_id) DO UPDATE SET title = EXCLUDED.title, length_mm = EXCLUDED.length_mm,
			width_mm = EXCLUDED.width_mm, height_mm = EXCLUDED.height_mm, weight_g = EXCLUDED.weight_g,
			hazmat = EXCLUDED.hazmat, fragile = EXCLUDED.fragile, updated_at = now()`,
		d.CatalogItemID, d.Title, d.LengthMm, d.WidthMm, d.HeightMm, d.WeightG, d.Hazmat, d.Fragile)
	return err
}

// Get implements ports.CatalogDimensionsView.
func (r *CatalogRepo) Get(ctx context.Context, id string) (repository.CatalogDimensions, error) {
	var d repository.CatalogDimensions
	err := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT catalog_item_id, title, length_mm, width_mm, height_mm, weight_g, hazmat, fragile
		FROM catalog_dimensions WHERE catalog_item_id = $1`, id).
		Scan(&d.CatalogItemID, &d.Title, &d.LengthMm, &d.WidthMm, &d.HeightMm, &d.WeightG, &d.Hazmat, &d.Fragile)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.CatalogDimensions{}, fmt.Errorf("%w: catalog item %s", ports.ErrNotFound, id)
	}
	return d, err
}
