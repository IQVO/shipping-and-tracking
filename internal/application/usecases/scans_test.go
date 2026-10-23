package usecases_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func restScan(tn shared.TrackingNumber, id string, typ journey.ScanType, h int) usecases.RecordScanCmd {
	return usecases.RecordScanCmd{TrackingNumber: tn, ScanID: id, NodeID: "node-1", Type: typ, At: hour(h)}
}

func TestRecordScanAppliesAndPublishesScannedAndHandedOver(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	v, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "s1", journey.ScanPickupDeparture, 1))
	noErr(t, err)
	if v.State != "HANDED_OVER" || len(v.Scans) != 1 || !v.Scans[0].Applied || v.HandedOverAt == nil || !v.HandedOverAt.Equal(hour(1)) {
		t.Fatalf("view = %+v", v)
	}
	if got := eventNames(r.store.Outbox().Events()); got != "LabelIssued,PackageScanned,PackageHandedOver" {
		t.Fatalf("events = %s", got)
	}
}

func TestRecordScanDeliveredPublishesTheMilestone(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery)
	v, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "d", journey.ScanDelivered, 9))
	noErr(t, err)
	if v.State != "DELIVERED" || v.DeliveredAt == nil || !v.DeliveredAt.Equal(hour(9)) {
		t.Fatalf("view = %+v", v)
	}
	names := eventNames(r.store.Outbox().Events())
	if names[len(names)-len("PackageScanned,PackageDelivered"):] != "PackageScanned,PackageDelivered" {
		t.Fatalf("events = %s", names)
	}
}

func TestRecordScanOutOfOrderIsStoredCommittedAndReported(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...)
	events := r.store.Counts().OutboxEvents
	v, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "old", journey.ScanStationArrival, 0))
	wantErr(t, err, journey.ErrScanOutOfOrder)
	if len(v.Scans) != 3 || v.Scans[2].Applied {
		t.Fatalf("the refusal still reports the journey with the stored scan, got %+v", v)
	}
	j := r.journey(t, tn)
	last := j.Scans()[len(j.Scans())-1]
	if last.ID != "old" || last.Applied || j.State() != journey.StateInTransit {
		t.Fatalf("stored scan = %+v in %s", last, j.State())
	}
	if r.store.Counts().OutboxEvents != events {
		t.Fatal("an unapplied scan publishes nothing")
	}
}

func TestRecordScanWithoutAnEdgeIsStoredAndIllegal(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	_, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "x", journey.ScanDelivered, 1))
	wantErr(t, err, shared.ErrIllegalTransition)
	if j := r.journey(t, tn); len(j.Scans()) != 1 || j.Scans()[0].Applied {
		t.Fatalf("scans = %+v", j.Scans())
	}
}

func TestRecordScanRefusalsThatStoreNothing(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, journey.ScanPickupDeparture)
	before := r.store.Counts()
	_, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "seed-PICKUP_DEPARTURE", journey.ScanDeparture, 5))
	wantErr(t, err, journey.ErrDuplicateScan)
	_, err = usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "", journey.ScanDeparture, 5))
	wantErr(t, err, shared.ErrValidation)
	_, err = usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan("RNNOPE", "z", journey.ScanDeparture, 5))
	wantErr(t, err, ports.ErrNotFound)
	if r.store.Counts() != before {
		t.Fatal("refused scans must change nothing")
	}
}

func TestRecordScanOnAClosedJourney(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	_, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "x")
	noErr(t, err)
	_, err = usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "late", journey.ScanPickupDeparture, 5))
	wantErr(t, err, journey.ErrJourneyClosed)
}

func TestRecordScanRetriesAConcurrentUpdateThenGivesUp(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	flaky := &flakyJourneys{JourneyRepository: r.store.Journeys(), failures: 2}
	r.deps.Journeys = flaky
	_, err := usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "s", journey.ScanPickupDeparture, 1))
	noErr(t, err)
	if flaky.saves != 3 {
		t.Fatalf("saves = %d, want 3 (two conflicts then success)", flaky.saves)
	}
	flaky.failures, flaky.saves = 3, 0
	_, err = usecases.NewRecordScan(r.deps).Execute(context.Background(), restScan(tn, "t", journey.ScanDeparture, 2))
	wantErr(t, err, ports.ErrConcurrentUpdate)
	if flaky.saves != 3 {
		t.Fatalf("saves = %d, want exactly 3 attempts", flaky.saves)
	}
}

func fact(tn shared.TrackingNumber, typ journey.ScanType, node string, h int) usecases.Fact {
	return usecases.Fact{TrackingNumber: tn, NodeID: node, Type: typ, At: hour(h)}
}

func TestApplyScansFoldsEveryTrackingNumberOfOneEvent(t *testing.T) {
	r := newRig(t)
	a := r.issued(t)
	cmd2 := issueCmd("PO-2", 1)
	v2, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), cmd2)
	noErr(t, err)
	b := shared.TrackingNumber(v2.TrackingNumber)
	res, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "linehaul", EventID: "ev-1",
		Facts: []usecases.Fact{fact(a, journey.ScanPickupDeparture, "fc-1", 1), fact(b, journey.ScanPickupDeparture, "fc-1", 1), fact("RNMISSING", journey.ScanDeparture, "fc-1", 1)},
	})
	noErr(t, err)
	if res != (usecases.ApplyResult{Applied: 2, Unknown: 1}) {
		t.Fatalf("result = %+v", res)
	}
	ja := r.journey(t, a)
	if ja.State() != journey.StateHandedOver || ja.Scans()[0].ID != "ev-1:"+string(a) || ja.Scans()[0].NodeID != "fc-1" {
		t.Fatalf("journey a = %s %+v", ja.State(), ja.Scans())
	}
}

func TestApplyScansSkipsAClaimedEvent(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	cmd := usecases.ApplyScansCmd{Consumer: "linehaul", EventID: "ev-1", Facts: []usecases.Fact{fact(tn, journey.ScanPickupDeparture, "fc-1", 1)}}
	_, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), cmd)
	noErr(t, err)
	before := r.store.Counts()
	res, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), cmd)
	noErr(t, err)
	if !res.Skipped || res.Applied != 0 || r.store.Counts() != before {
		t.Fatalf("result=%+v counts=%+v", res, r.store.Counts())
	}
	other := cmd
	other.Consumer, other.EventID = "lastmile", "ev-2"
	other.Facts = []usecases.Fact{fact(tn, journey.ScanDeparture, "fc-1", 2)}
	res, err = usecases.NewApplyScans(r.deps).Execute(context.Background(), other)
	noErr(t, err)
	if res.Skipped || res.Applied != 1 {
		t.Fatalf("a claim is per consumer, got %+v", res)
	}
}

func TestApplyScansAcknowledgesDomainRefusalsAndCountsThem(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toInTransit...)
	v2, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-2", 1))
	noErr(t, err)
	closed := shared.TrackingNumber(v2.TrackingNumber)
	_, err = usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-2", "x")
	noErr(t, err)
	for i, c := range []struct {
		name string
		f    usecases.Fact
		want usecases.ApplyResult
	}{
		{"out of order is stored", fact(tn, journey.ScanStationArrival, "ds-1", 0), usecases.ApplyResult{Stored: 1}},
		{"no edge is stored", fact(tn, journey.ScanDelivered, "ds-1", 20), usecases.ApplyResult{Stored: 1}},
		{"closed is counted", fact(closed, journey.ScanDeparture, "ds-1", 20), usecases.ApplyResult{Closed: 1}},
		{"applied", fact(tn, journey.ScanDeparture, "ds-1", 21), usecases.ApplyResult{Applied: 1}},
	} {
		res, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
			Consumer: "c", EventID: fmt.Sprintf("ev-%d", i+1), Facts: []usecases.Fact{c.f},
		})
		noErr(t, err)
		if res != c.want {
			t.Errorf("%s: result = %+v, want %+v", c.name, res, c.want)
		}
	}
	// The same fact under a new consumer and the same event id repeats the scan id.
	res, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "other", EventID: "ev-4", Facts: []usecases.Fact{fact(tn, journey.ScanDeparture, "ds-1", 21)},
	})
	noErr(t, err)
	if res != (usecases.ApplyResult{Duplicates: 1}) {
		t.Fatalf("a repeated scan id is a duplicate, got %+v", res)
	}
}

func TestApplyScansPromotesADepartureOfALabelledJourneyAndFallsBackOnTheNode(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	res, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "c", EventID: "ev-1",
		Facts: []usecases.Fact{{TrackingNumber: tn, NodeID: "fc-1", Type: journey.ScanDeparture, At: hour(1), PickupIfLabelled: true}},
	})
	noErr(t, err)
	if res.Applied != 1 || r.journey(t, tn).State() != journey.StateHandedOver {
		t.Fatalf("result=%+v state=%s", res, r.journey(t, tn).State())
	}
	// Now handed over: a promoted departure stays a plain DEPARTURE.
	_, err = usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "c", EventID: "ev-2",
		Facts: []usecases.Fact{{TrackingNumber: tn, NodeID: "sc-1", Type: journey.ScanDeparture, At: hour(2), PickupIfLabelled: true}},
	})
	noErr(t, err)
	if s := r.journey(t, tn).Scans(); s[1].Type != journey.ScanDeparture {
		t.Fatalf("scans = %+v", s)
	}
	// An empty node takes the node of the last applied scan.
	_, err = usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "c", EventID: "ev-3", Facts: []usecases.Fact{{TrackingNumber: tn, Type: journey.ScanStationArrival, At: hour(3)}},
	})
	noErr(t, err)
	if s := r.journey(t, tn).Scans(); s[2].NodeID != "sc-1" {
		t.Fatalf("scans = %+v", s)
	}
}

func TestApplyScansWithNoNodeAnywhereStoresTheScanOnTheUnknownNode(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	res, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "c", EventID: "ev-1", Facts: []usecases.Fact{{TrackingNumber: tn, Type: journey.ScanDelivered, At: hour(1)}},
	})
	noErr(t, err)
	s := r.journey(t, tn).Scans()
	if res.Stored != 1 || s[0].NodeID != "unknown" || s[0].Applied {
		t.Fatalf("result=%+v scans=%+v", res, s)
	}
}

func TestApplyScansValidationFailureRollsBackTheClaim(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	bad := usecases.ApplyScansCmd{Consumer: "c", EventID: "ev-1", Facts: []usecases.Fact{
		fact(tn, journey.ScanPickupDeparture, "fc-1", 1), {TrackingNumber: tn, NodeID: "fc-1", Type: journey.ScanDeparture},
	}}
	before := r.store.Counts()
	_, err := usecases.NewApplyScans(r.deps).Execute(context.Background(), bad)
	wantErr(t, err, shared.ErrValidation)
	if r.store.Counts() != before {
		t.Fatalf("the whole event must roll back, counts = %+v", r.store.Counts())
	}
	ok, err := r.store.Inbox().Claim(context.Background(), "c", "ev-1")
	noErr(t, err)
	if !ok {
		t.Fatal("the claim must be free again")
	}
}

func TestReturnToSender(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	cmd := usecases.ReturnCmd{Consumer: "lastmile", EventID: "ev-r", TrackingNumber: tn, ReturnedAt: hour(9)}
	_, err := usecases.NewReturnToSender(r.deps).Execute(context.Background(), cmd)
	wantErr(t, err, shared.ErrIllegalTransition)
	if ok, _ := r.store.Inbox().Claim(context.Background(), "lastmile", "ev-r"); !ok {
		t.Fatal("a refused return releases its claim so the consumer can retry")
	}
	r2 := newRig(t)
	tn = r2.issued(t)
	r2.scanTo(t, tn, toAtStation...)
	res, err := usecases.NewReturnToSender(r2.deps).Execute(context.Background(), cmd)
	noErr(t, err)
	if !res.Returned || r2.journey(t, tn).State() != journey.StateReturned {
		t.Fatalf("result=%+v", res)
	}
	if names := eventNames(r2.store.Outbox().Events()); names[len(names)-len("PackageReturned"):] != "PackageReturned" {
		t.Fatalf("events = %s", names)
	}
	again, err := usecases.NewReturnToSender(r2.deps).Execute(context.Background(), cmd)
	noErr(t, err)
	if !again.Skipped {
		t.Fatalf("a claimed event is skipped, got %+v", again)
	}
	closed, err := usecases.NewReturnToSender(r2.deps).Execute(context.Background(), usecases.ReturnCmd{
		Consumer: "lastmile", EventID: "ev-r2", TrackingNumber: tn, ReturnedAt: hour(10),
	})
	noErr(t, err)
	if !closed.Closed {
		t.Fatalf("a closed journey is acknowledged, got %+v", closed)
	}
	unknown, err := usecases.NewReturnToSender(r2.deps).Execute(context.Background(), usecases.ReturnCmd{
		Consumer: "lastmile", EventID: "ev-r3", TrackingNumber: "RNNOPE", ReturnedAt: hour(10),
	})
	noErr(t, err)
	if !unknown.Unknown {
		t.Fatalf("an unknown tracking number is acknowledged, got %+v", unknown)
	}
}
