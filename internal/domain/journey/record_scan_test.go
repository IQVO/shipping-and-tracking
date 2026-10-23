package journey

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestStartCreatesALabelledJourneyWithoutEvents(t *testing.T) {
	j, err := Start(newJourneyIn(), t0.In(time.FixedZone("x", -3*3600)))
	mustNoErr(t, err)
	checks := []struct {
		name      string
		got, want any
	}{
		{"tracking", j.TrackingNumber(), TrackingNumber("RN0123456789ABCDEF")}, {"labelRef", j.LabelRef(), "lbl-1"},
		{"po", j.PONumber(), "PO-1"}, {"assignment", j.AssignmentID(), "asg-1"}, {"order", j.OrderID(), "ord-1"},
		{"tier", j.Tier(), shared.TierNextDay}, {"state", j.State(), StateLabelled}, {"startedAt", j.StartedAt(), t0},
		{"version", j.Version(), int64(1)}, {"scans", len(j.Scans()), 0}, {"lastScanAt", j.LastScanAt(), time.Time{}},
		{"lastNode", j.LastNodeID(), ""}, {"handedOverAt", j.HandedOverAt(), time.Time{}},
		{"deliveredAt", j.DeliveredAt(), time.Time{}}, {"closedAt", j.ClosedAt(), time.Time{}},
		{"lastExceptionAt", j.LastExceptionAt(), time.Time{}},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if j.StartedAt().Location() != time.UTC {
		t.Error("startedAt must be UTC")
	}
}

func TestStartRefusesInvalidInput(t *testing.T) {
	cases := map[string]func(*NewJourney){
		"tracking":   func(n *NewJourney) { n.TrackingNumber = "" },
		"labelRef":   func(n *NewJourney) { n.LabelRef = "a b" },
		"po":         func(n *NewJourney) { n.PONumber = "" },
		"assignment": func(n *NewJourney) { n.AssignmentID = "" },
		"order":      func(n *NewJourney) { n.OrderID = strings.Repeat("o", shared.MaxID+1) },
		"tier":       func(n *NewJourney) { n.Tier = "FAST" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			in := newJourneyIn()
			mut(&in)
			if j, err := Start(in, t0); !errors.Is(err, shared.ErrValidation) || j != nil {
				t.Fatalf("want ErrValidation, got %v %v", j, err)
			}
		})
	}
}

// legalEdges is the model's RecordScan table plus the direct-injection edge,
// written out independently of next() so a change to either is caught.
var legalEdges = map[ScanType]map[State]State{
	ScanShipmentConfirmed: {StateLabelled: StateLabelled},
	ScanPickupDeparture:   {StateLabelled: StateHandedOver},
	ScanDeparture:         {StateHandedOver: StateInTransit, StateInTransit: StateInTransit},
	ScanTransitArrival:    {StateHandedOver: StateInTransit, StateInTransit: StateInTransit},
	ScanStationArrival:    {StateHandedOver: StateAtStation, StateInTransit: StateAtStation},
	ScanOutForDelivery:    {StateAtStation: StateOutForDelivery},
	ScanDelivered:         {StateOutForDelivery: StateDelivered},
	ScanDeliveryFailed:    {StateOutForDelivery: StateAtStation},
}

var openStates = []State{StateLabelled, StateHandedOver, StateInTransit, StateAtStation, StateOutForDelivery}

func TestEveryScanTypeAgainstEveryOpenState(t *testing.T) {
	for _, typ := range AllScanTypes() {
		for _, from := range openStates {
			t.Run(string(typ)+"_from_"+string(from), func(t *testing.T) {
				j := inState(t, from)
				before := j.Version()
				res, err := j.RecordScan(scan("probe", typ, 30), at(31))
				if to, legal := legalEdges[typ][from]; legal {
					assertApplied(t, j, res, err, to, before)
					return
				}
				assertStoredNotApplied(t, j, res, err, from, before)
			})
		}
	}
}

func assertApplied(t *testing.T, j *Journey, res Result, err error, to State, before int64) {
	t.Helper()
	mustNoErr(t, err)
	if j.State() != to || !res.Changed || j.Version() != before+1 {
		t.Fatalf("state=%s changed=%v version=%d, want %s true %d", j.State(), res.Changed, j.Version(), to, before+1)
	}
	last := j.Scans()[len(j.Scans())-1]
	if last.ID != "probe" || !last.Applied || !last.At.Equal(at(30)) || j.LastScanAt() != at(30) {
		t.Fatalf("stored scan = %+v lastScanAt=%v", last, j.LastScanAt())
	}
	if len(res.Events) == 0 || res.Events[0].EventName() != "PackageScanned" {
		t.Fatalf("first event must be PackageScanned, got %v", eventNames(res.Events))
	}
}

func assertStoredNotApplied(t *testing.T, j *Journey, res Result, err error, from State, before int64) {
	t.Helper()
	if !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	if j.State() != from || !res.Changed || res.Events != nil || j.Version() != before+1 {
		t.Fatalf("state=%s changed=%v events=%v version=%d", j.State(), res.Changed, res.Events, j.Version())
	}
	last := j.Scans()[len(j.Scans())-1]
	if last.ID != "probe" || last.Applied {
		t.Fatalf("scan must be kept unapplied, got %+v", last)
	}
	if j.LastScanAt().Equal(at(30)) {
		t.Fatal("an unapplied scan must not move lastScanAt")
	}
}

func TestTerminalStatesRefuseEveryScan(t *testing.T) {
	for _, st := range []State{StateDelivered, StateReturned, StateLost, StateVoided} {
		for _, typ := range AllScanTypes() {
			t.Run(string(st)+"_"+string(typ), func(t *testing.T) {
				j := inState(t, st)
				before, n := j.Version(), len(j.Scans())
				res, err := j.RecordScan(scan("late", typ, 40), at(41))
				if !errors.Is(err, ErrJourneyClosed) || res.Changed || res.Events != nil {
					t.Fatalf("want ErrJourneyClosed unchanged, got %v %+v", err, res)
				}
				if j.Version() != before || len(j.Scans()) != n || j.State() != st {
					t.Fatal("a closed journey must not change")
				}
			})
		}
	}
}

func TestMilestoneEventsOnTheEdgesThatRaiseThem(t *testing.T) {
	j := started(t)
	res, err := j.RecordScan(scan("a", ScanShipmentConfirmed, 1), at(1))
	mustNoErr(t, err)
	if !sameNames(eventNames(res.Events), []string{"PackageScanned"}) || j.State() != StateLabelled {
		t.Fatalf("shipment confirmed: %v in %s", eventNames(res.Events), j.State())
	}
	res, err = j.RecordScan(scan("b", ScanPickupDeparture, 2), at(2))
	mustNoErr(t, err)
	if !sameNames(eventNames(res.Events), []string{"PackageScanned", "PackageHandedOver"}) {
		t.Fatalf("pickup: %v", eventNames(res.Events))
	}
	ho := res.Events[1].(PackageHandedOver)
	if ho.TrackingNumber != "RN0123456789ABCDEF" || ho.PONumber != "PO-1" || ho.NodeID != "node-PICKUP_DEPARTURE" ||
		!ho.HandedOverAt.Equal(at(2)) || j.HandedOverAt() != at(2) {
		t.Fatalf("handed over event = %+v", ho)
	}
	driveRest(t, j)
}

func driveRest(t *testing.T, j *Journey) {
	t.Helper()
	for i, typ := range []ScanType{ScanDeparture, ScanStationArrival, ScanOutForDelivery} {
		res, err := j.RecordScan(scan("r"+string(typ), typ, 3+i), at(3+i))
		mustNoErr(t, err)
		if !sameNames(eventNames(res.Events), []string{"PackageScanned"}) {
			t.Fatalf("%s: %v", typ, eventNames(res.Events))
		}
	}
	res, err := j.RecordScan(scan("d", ScanDelivered, 6), at(7))
	mustNoErr(t, err)
	if !sameNames(eventNames(res.Events), []string{"PackageScanned", "PackageDelivered"}) {
		t.Fatalf("delivered: %v", eventNames(res.Events))
	}
	d := res.Events[1].(PackageDelivered)
	if d.AssignmentID != "asg-1" || d.OrderID != "ord-1" || !d.DeliveredAt.Equal(at(6)) || j.DeliveredAt() != at(6) {
		t.Fatalf("delivered event = %+v", d)
	}
	if j.ClosedAt() != at(7) || d.OccurredAt() != at(7) {
		t.Fatalf("closedAt=%v occurredAt=%v, want the command clock %v", j.ClosedAt(), d.OccurredAt(), at(7))
	}
}

func TestPackageScannedCarriesTheStateAfterTheScan(t *testing.T) {
	j := inState(t, StateOutForDelivery)
	res, err := j.RecordScan(scan("f", ScanDeliveryFailed, 9), at(10))
	mustNoErr(t, err)
	ev := res.Events[0].(PackageScanned)
	if ev.State != StateAtStation || ev.ScanID != "f" || ev.NodeID != "node-DELIVERY_FAILED" || ev.ScanType != ScanDeliveryFailed ||
		!ev.ScannedAt.Equal(at(9)) || ev.TrackingNumber != "RN0123456789ABCDEF" || ev.OccurredAt() != at(10) ||
		ev.AggregateVersion() != j.Version() || ev.Entity() != "journey" || ev.SubjectID() != "RN0123456789ABCDEF" {
		t.Fatalf("event = %+v", ev)
	}
	if j.LastNodeID() != "node-DELIVERY_FAILED" {
		t.Fatalf("lastNode = %s", j.LastNodeID())
	}
}

func TestDuplicateScanIsRefusedAndStoresNothing(t *testing.T) {
	j := inState(t, StateInTransit)
	before, n := j.Version(), len(j.Scans())
	res, err := j.RecordScan(scan("sPICKUP_DEPARTURE", ScanDeparture, 30), at(31))
	if !errors.Is(err, ErrDuplicateScan) || res.Changed || res.Events != nil || j.Version() != before || len(j.Scans()) != n {
		t.Fatalf("duplicate: %v %+v v%d n%d", err, res, j.Version(), len(j.Scans()))
	}
}

func TestDuplicateWinsOverOutOfOrder(t *testing.T) {
	j := inState(t, StateInTransit)
	_, err := j.RecordScan(scan("sPICKUP_DEPARTURE", ScanDeparture, 0), at(31))
	if !errors.Is(err, ErrDuplicateScan) {
		t.Fatalf("want ErrDuplicateScan, got %v", err)
	}
}

func TestOutOfOrderScanIsStoredNotApplied(t *testing.T) {
	j := inState(t, StateInTransit) // last applied scan at hour 2
	before := j.Version()
	res, err := j.RecordScan(scan("old", ScanStationArrival, 1), at(9))
	if !errors.Is(err, ErrScanOutOfOrder) || !res.Changed || res.Events != nil {
		t.Fatalf("want ErrScanOutOfOrder with Changed, got %v %+v", err, res)
	}
	if j.State() != StateInTransit || j.LastScanAt() != at(2) || j.Version() != before+1 {
		t.Fatalf("state=%s lastScanAt=%v v%d", j.State(), j.LastScanAt(), j.Version())
	}
	if last := j.Scans()[len(j.Scans())-1]; last.ID != "old" || last.Applied {
		t.Fatalf("stored scan = %+v", last)
	}
}

func TestOutOfOrderBoundaryEqualInstantApplies(t *testing.T) {
	j := inState(t, StateInTransit) // last applied scan at hour 2
	if _, err := j.RecordScan(scan("same", ScanTransitArrival, 2), at(3)); err != nil {
		t.Fatalf("a scan at exactly the last applied instant is in order: %v", err)
	}
	j2 := inState(t, StateInTransit)
	one := ScanInput{ID: "early", NodeID: "n", Type: ScanTransitArrival, At: at(2).Add(-time.Nanosecond)}
	if _, err := j2.RecordScan(one, at(3)); !errors.Is(err, ErrScanOutOfOrder) {
		t.Fatalf("one nanosecond older must be out of order, got %v", err)
	}
}

func TestOutOfOrderWinsOverMissingEdge(t *testing.T) {
	j := inState(t, StateInTransit)
	_, err := j.RecordScan(scan("old", ScanDelivered, 1), at(9))
	if !errors.Is(err, ErrScanOutOfOrder) || errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("want only ErrScanOutOfOrder, got %v", err)
	}
}

func TestFirstScanHasNoPredecessorToBeOlderThan(t *testing.T) {
	j := started(t)
	if _, err := j.RecordScan(scan("early", ScanShipmentConfirmed, -100), at(1)); err != nil {
		t.Fatalf("the first applied scan may carry any instant: %v", err)
	}
}

func TestRecordScanValidatesItsInput(t *testing.T) {
	good := scan("ok", ScanShipmentConfirmed, 1)
	cases := map[string]func(*ScanInput){
		"empty id":    func(s *ScanInput) { s.ID = "" },
		"long id":     func(s *ScanInput) { s.ID = strings.Repeat("a", shared.MaxScanID+1) },
		"space id":    func(s *ScanInput) { s.ID = "a b" },
		"empty node":  func(s *ScanInput) { s.NodeID = "" },
		"long node":   func(s *ScanInput) { s.NodeID = strings.Repeat("n", shared.MaxID+1) },
		"unknown typ": func(s *ScanInput) { s.Type = "TELEPORT" },
		"zero time":   func(s *ScanInput) { s.At = time.Time{} },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			j := started(t)
			in := good
			mut(&in)
			res, err := j.RecordScan(in, at(2))
			if !errors.Is(err, shared.ErrValidation) || res.Changed || j.Version() != 1 {
				t.Fatalf("want ErrValidation unchanged, got %v %+v v%d", err, res, j.Version())
			}
		})
	}
	long := good
	long.ID = strings.Repeat("a", shared.MaxScanID)
	if _, err := started(t).RecordScan(long, at(2)); err != nil {
		t.Fatalf("a scan id of exactly MaxScanID characters is accepted: %v", err)
	}
}

func TestValidationRunsBeforeTheClosedCheck(t *testing.T) {
	j := inState(t, StateDelivered)
	_, err := j.RecordScan(ScanInput{}, at(50))
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("want ErrValidation first, got %v", err)
	}
}

func TestScansReturnsACopy(t *testing.T) {
	j := inState(t, StateHandedOver)
	got := j.Scans()
	got[0].ID = "tampered"
	if j.Scans()[0].ID == "tampered" {
		t.Fatal("Scans must return a copy")
	}
}
