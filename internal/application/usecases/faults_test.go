package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var errInfra = errors.New("infrastructure down")

// faults switches a failure on for one port call.
type faults struct {
	labelGet, labelFind, labelSave, snapshotPut, snapshotPurge   bool
	journeyGet, journeySave, outbox, claim, catalogUpsert, stale bool
}

type faultyLabels struct {
	ports.LabelRepository
	f *faults
}

func (l faultyLabels) Get(ctx context.Context, ref label.Ref) (*label.ShippingLabel, error) {
	if l.f.labelGet {
		return nil, errInfra
	}
	return l.LabelRepository.Get(ctx, ref)
}

func (l faultyLabels) FindByPackage(ctx context.Context, po string, n int) (*label.ShippingLabel, error) {
	if l.f.labelFind {
		return nil, errInfra
	}
	return l.LabelRepository.FindByPackage(ctx, po, n)
}

func (l faultyLabels) Save(ctx context.Context, x *label.ShippingLabel) error {
	if l.f.labelSave {
		return errInfra
	}
	return l.LabelRepository.Save(ctx, x)
}

type faultySnapshots struct {
	ports.ShipToSnapshots
	f *faults
}

func (s faultySnapshots) Put(ctx context.Context, ref label.Ref, v repository.SealedShipTo) error {
	if s.f.snapshotPut {
		return errInfra
	}
	return s.ShipToSnapshots.Put(ctx, ref, v)
}

func (s faultySnapshots) PurgeClosedBefore(ctx context.Context, c time.Time) (int, error) {
	if s.f.snapshotPurge {
		return 0, errInfra
	}
	return s.ShipToSnapshots.PurgeClosedBefore(ctx, c)
}

type faultyJourneys struct {
	ports.JourneyRepository
	f *faults
}

func (j faultyJourneys) Get(ctx context.Context, tn shared.TrackingNumber) (*journey.Journey, error) {
	if j.f.journeyGet {
		return nil, errInfra
	}
	return j.JourneyRepository.Get(ctx, tn)
}

func (j faultyJourneys) Save(ctx context.Context, x *journey.Journey) error {
	if j.f.journeySave {
		return errInfra
	}
	return j.JourneyRepository.Save(ctx, x)
}

func (j faultyJourneys) ListStale(ctx context.Context, f repository.StaleFilter) ([]*journey.Journey, error) {
	if j.f.stale {
		return nil, errInfra
	}
	return j.JourneyRepository.ListStale(ctx, f)
}

type faultyOutbox struct {
	ports.EventOutbox
	f *faults
}

func (o faultyOutbox) Append(ctx context.Context, evs ...shared.Event) error {
	if o.f.outbox {
		return errInfra
	}
	return o.EventOutbox.Append(ctx, evs...)
}

type faultyInbox struct {
	ports.ProcessedEvents
	f *faults
}

func (i faultyInbox) Claim(ctx context.Context, c, id string) (bool, error) {
	if i.f.claim {
		return false, errInfra
	}
	return i.ProcessedEvents.Claim(ctx, c, id)
}

type faultyCatalog struct {
	ports.CatalogDimensionsView
	f *faults
}

func (c faultyCatalog) Upsert(ctx context.Context, d repository.CatalogDimensions) error {
	if c.f.catalogUpsert {
		return errInfra
	}
	return c.CatalogDimensionsView.Upsert(ctx, d)
}

// faulty wraps every port of the rig; the returned faults start all off.
func (r *rig) faulty() *faults {
	f := &faults{}
	d := r.deps
	r.deps.Labels, r.deps.Snapshots, r.deps.Journeys = faultyLabels{d.Labels, f}, faultySnapshots{d.Snapshots, f}, faultyJourneys{d.Journeys, f}
	r.deps.Outbox, r.deps.Inbox, r.deps.Catalog = faultyOutbox{d.Outbox, f}, faultyInbox{d.Inbox, f}, faultyCatalog{d.Catalog, f}
	return f
}

// withFault runs op with one fault flag on and expects errInfra and no change.
func withFault(t *testing.T, name string, r *rig, set func(*faults), op func() error) {
	t.Helper()
	f := r.deps.Labels.(faultyLabels).f
	before := r.store.Counts()
	*f = faults{}
	set(f)
	err := op()
	*f = faults{}
	if !errors.Is(err, errInfra) {
		t.Errorf("%s: want errInfra, got %v", name, err)
	}
	if r.store.Counts() != before {
		t.Errorf("%s: a failed use case must change nothing, counts %+v -> %+v", name, before, r.store.Counts())
	}
}

func TestIssueLabelSurfacesEveryInfrastructureFailureAndStoresNothing(t *testing.T) {
	r := newRig(t)
	r.faulty()
	run := func() error {
		_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
		return err
	}
	withFault(t, "find", r, func(f *faults) { f.labelFind = true }, run)
	withFault(t, "label save", r, func(f *faults) { f.labelSave = true }, run)
	withFault(t, "snapshot put", r, func(f *faults) { f.snapshotPut = true }, run)
	withFault(t, "journey save", r, func(f *faults) { f.journeySave = true }, run)
	withFault(t, "outbox", r, func(f *faults) { f.outbox = true }, run)
}

func TestVoidLabelSurfacesEveryInfrastructureFailureAndChangesNothing(t *testing.T) {
	r := newRig(t)
	r.issued(t)
	r.faulty()
	run := func() error {
		_, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "x")
		return err
	}
	withFault(t, "label get", r, func(f *faults) { f.labelGet = true }, run)
	withFault(t, "journey get", r, func(f *faults) { f.journeyGet = true }, run)
	withFault(t, "label save", r, func(f *faults) { f.labelSave = true }, run)
	withFault(t, "journey save", r, func(f *faults) { f.journeySave = true }, run)
	withFault(t, "outbox", r, func(f *faults) { f.outbox = true }, run)
}

func TestScanUseCasesSurfaceEveryInfrastructureFailureAndChangeNothing(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toAtStation...)
	r.faulty()
	rest := func() error {
		_, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "x1", journey.ScanOutForDelivery, 9))
		return err
	}
	withFault(t, "rest get", r, func(f *faults) { f.journeyGet = true }, rest)
	withFault(t, "rest save", r, func(f *faults) { f.journeySave = true }, rest)
	withFault(t, "rest outbox", r, func(f *faults) { f.outbox = true }, rest)

	apply := func() error {
		_, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
			Consumer: "c", EventID: "e1", Facts: []usecases.Fact{fact(tn, journey.ScanOutForDelivery, "ds", 9)},
		})
		return err
	}
	withFault(t, "apply claim", r, func(f *faults) { f.claim = true }, apply)
	withFault(t, "apply get", r, func(f *faults) { f.journeyGet = true }, apply)
	withFault(t, "apply save", r, func(f *faults) { f.journeySave = true }, apply)
	withFault(t, "apply outbox", r, func(f *faults) { f.outbox = true }, apply)

	ret := func() error {
		_, err := usecases.NewReturnToSender(r.deps).Execute(context.Background(), usecases.ReturnCmd{
			Consumer: "c", EventID: "e2", TrackingNumber: tn, ReturnedAt: hour(9),
		})
		return err
	}
	withFault(t, "return claim", r, func(f *faults) { f.claim = true }, ret)
	withFault(t, "return get", r, func(f *faults) { f.journeyGet = true }, ret)
	withFault(t, "return save", r, func(f *faults) { f.journeySave = true }, ret)
	withFault(t, "return outbox", r, func(f *faults) { f.outbox = true }, ret)
}

func TestSweepAndHousekeepingSurfaceInfrastructureFailures(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...)
	r.faulty()
	r.clock.Set(hour(100))
	sweep := func() error { _, err := usecases.NewSweepSilence(r.deps).Execute(context.Background()); return err }
	withFault(t, "stale", r, func(f *faults) { f.stale = true }, sweep)
	withFault(t, "sweep get", r, func(f *faults) { f.journeyGet = true }, sweep)
	withFault(t, "sweep save", r, func(f *faults) { f.journeySave = true }, sweep)
	withFault(t, "sweep outbox", r, func(f *faults) { f.outbox = true }, sweep)

	withFault(t, "purge", r, func(f *faults) { f.snapshotPurge = true }, func() error {
		_, err := usecases.NewPurgeShipTo(r.deps).Execute(context.Background())
		return err
	})
	catalog := func() error {
		_, err := usecases.NewRecordCatalogItem(r.deps).Execute(context.Background(), "c", "e", repository.CatalogDimensions{CatalogItemID: "i1"})
		return err
	}
	withFault(t, "catalog claim", r, func(f *faults) { f.claim = true }, catalog)
	withFault(t, "catalog upsert", r, func(f *faults) { f.catalogUpsert = true }, catalog)
}
