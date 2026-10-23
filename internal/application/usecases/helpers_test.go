package usecases_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

func hour(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }

// rig is one wired application over the in-memory doubles.
type rig struct {
	store   *memory.Store
	clock   *memory.Clock
	ids     *memory.SequentialIDs
	gateway *memory.ShipToGateway
	deps    usecases.Deps
}

func newRig(t *testing.T) *rig {
	t.Helper()
	s := memory.NewStore()
	r := &rig{store: s, clock: memory.NewClock(t0), ids: &memory.SequentialIDs{}, gateway: memory.NewShipToGateway()}
	r.gateway.Put("ord-1", sampleAddress())
	r.deps = usecases.Deps{
		Labels: s.Labels(), Snapshots: s.Snapshots(), Journeys: s.Journeys(), Catalog: s.Catalog(), Inbox: s.Inbox(),
		Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: r.clock, IDs: r.ids, ShipTo: r.gateway,
		Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
	}
	return r
}

func sampleAddress() shipto.Address {
	return shipto.Address{
		Name: "Ana Example", Line1: "10 Sample Street", Line2: "Apt 4", City: "Sampletown", Region: "SP",
		PostalCode: "01310-100", CountryCode: "BR", Phone: "+55 11 5550 0100",
	}
}

func issueCmd(po string, pkg int) usecases.IssueLabelCmd {
	return usecases.IssueLabelCmd{PONumber: po, PackageNo: pkg, AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay}
}

// issued issues the first label (lbl-1, RN000000000000001) and returns its tracking number.
func (r *rig) issued(t *testing.T) shared.TrackingNumber {
	t.Helper()
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	if err != nil {
		t.Fatalf("IssueLabel: %v", err)
	}
	return shared.TrackingNumber(v.TrackingNumber)
}

// scanTo drives the journey with REST scans, one hour apart from hour 1.
func (r *rig) scanTo(t *testing.T, tn shared.TrackingNumber, types ...journey.ScanType) {
	t.Helper()
	for i, typ := range types {
		_, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), usecases.RecordScanCmd{
			TrackingNumber: tn, ScanID: "seed-" + string(typ), NodeID: "node-" + string(typ), Type: typ, At: hour(i + 1),
		})
		if err != nil {
			t.Fatalf("scan %s: %v", typ, err)
		}
	}
}

var (
	toInTransit = []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture}
	toAtStation = []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival}
)

func (r *rig) journey(t *testing.T, tn shared.TrackingNumber) *journey.Journey {
	t.Helper()
	j, err := r.store.Journeys().Get(context.Background(), tn)
	if err != nil {
		t.Fatalf("load journey: %v", err)
	}
	return j
}

func eventNames(evs []shared.Event) string {
	names := make([]string, len(evs))
	for i, e := range evs {
		names[i] = e.EventName()
	}
	return strings.Join(names, ",")
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// flakyJourneys makes the first n Saves fail with ErrConcurrentUpdate.
type flakyJourneys struct {
	ports.JourneyRepository
	failures int
	saves    int
}

func (f *flakyJourneys) Save(ctx context.Context, j *journey.Journey) error {
	f.saves++
	if f.failures > 0 {
		f.failures--
		return ports.ErrConcurrentUpdate
	}
	return f.JourneyRepository.Save(ctx, j)
}
