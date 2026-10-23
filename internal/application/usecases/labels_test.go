package usecases_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestIssueLabelStoresLabelSnapshotJourneyAndEvent(t *testing.T) {
	r := newRig(t)
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	assertIssuedView(t, v)
	c := r.store.Counts()
	if c.Labels != 1 || c.Snapshots != 1 || c.Journeys != 1 || c.OutboxEvents != 1 || c.Scans != 0 {
		t.Fatalf("counts = %+v", c)
	}
	j := r.journey(t, "RN000000000000001")
	if j.State() != journey.StateLabelled || j.LabelRef() != "lbl-1" || j.Tier() != shared.TierNextDay {
		t.Fatalf("journey = %s %s %s", j.State(), j.LabelRef(), j.Tier())
	}
	assertIssuedEvent(t, r.store.Outbox().Events()[0].(label.LabelIssued))
	if calls := r.gateway.Calls(); len(calls) != 1 || calls[0] != "ord-1" {
		t.Fatalf("gateway calls = %v", calls)
	}
}

func assertIssuedView(t *testing.T, v views.Label) {
	t.Helper()
	if v.LabelRef != "lbl-1" || v.TrackingNumber != "RN000000000000001" || v.Carrier != label.CarrierExpress ||
		v.Status != "ISSUED" || v.PONumber != "PO-1" || v.PackageNo != 1 || v.AssignmentID != "asg-1" ||
		v.OrderID != "ord-1" || v.Tier != "NEXT_DAY" || !v.IssuedAt.Equal(t0) || v.VoidedAt != nil || v.VoidReason != "" {
		t.Fatalf("view = %+v", v)
	}
}

func assertIssuedEvent(t *testing.T, ev label.LabelIssued) {
	t.Helper()
	if ev.StopKey != (memory.HashKeyer{}).StopKey(sampleAddress().Normalized()) || ev.DestinationPostalPrefix != "01310" ||
		ev.OrderID != "ord-1" || ev.Carrier != label.CarrierExpress {
		t.Fatalf("event = %+v", ev)
	}
}

func TestIssueLabelEventAndViewCarryNoAddress(t *testing.T) {
	r := newRig(t)
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	ev := r.store.Outbox().Events()[0]
	for _, s := range []string{fmtAll(v), fmtAll(ev)} {
		for _, pii := range []string{"Sample Street", "Sampletown", "Ana Example", "01310-100", "5550"} {
			if strings.Contains(s, pii) {
				t.Fatalf("%q leaked into %s", pii, s)
			}
		}
	}
}

func TestSameAddressGivesTheSameStopKeyAndAnotherNameDoesNot(t *testing.T) {
	r := newRig(t)
	other := sampleAddress()
	other.Name, other.Phone, other.Line1 = "Bia Example", "", "10  SAMPLE street."
	r.gateway.Put("ord-2", other)
	cmd2 := issueCmd("PO-2", 1)
	cmd2.OrderID = "ord-2"
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	_, err = usecases.NewIssueLabel(r.deps).Execute(context.Background(), cmd2)
	noErr(t, err)
	evs := r.store.Outbox().Events()
	if evs[0].(label.LabelIssued).StopKey != evs[1].(label.LabelIssued).StopKey {
		t.Fatal("neighbours at one address share a stop")
	}
	moved := sampleAddress()
	moved.Line1 = "11 Sample Street"
	r.gateway.Put("ord-3", moved)
	cmd3 := issueCmd("PO-3", 1)
	cmd3.OrderID = "ord-3"
	_, err = usecases.NewIssueLabel(r.deps).Execute(context.Background(), cmd3)
	noErr(t, err)
	if r.store.Outbox().Events()[2].(label.LabelIssued).StopKey == evs[0].(label.LabelIssued).StopKey {
		t.Fatal("another address is another stop")
	}
}

func TestIssueLabelRefusesInvalidCommandsBeforeAnyCall(t *testing.T) {
	r := newRig(t)
	for name, mut := range map[string]func(*usecases.IssueLabelCmd){
		"po":        func(c *usecases.IssueLabelCmd) { c.PONumber = "" },
		"package":   func(c *usecases.IssueLabelCmd) { c.PackageNo = 0 },
		"assigment": func(c *usecases.IssueLabelCmd) { c.AssignmentID = "a b" },
		"order":     func(c *usecases.IssueLabelCmd) { c.OrderID = "" },
		"tier":      func(c *usecases.IssueLabelCmd) { c.Tier = "FAST" },
	} {
		cmd := issueCmd("PO-1", 1)
		mut(&cmd)
		_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), cmd)
		if !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
	if len(r.gateway.Calls()) != 0 || r.store.Counts().Labels != 0 {
		t.Fatal("nothing may be called or stored for an invalid command")
	}
}

func TestIssueLabelTwiceForOnePackageIsRefusedBeforeTheGatewayCall(t *testing.T) {
	r := newRig(t)
	r.issued(t)
	before := r.store.Counts()
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	wantErr(t, err, label.ErrLabelAlreadyIssued)
	if len(r.gateway.Calls()) != 1 || r.store.Counts() != before {
		t.Fatalf("calls=%v counts=%+v", r.gateway.Calls(), r.store.Counts())
	}
	_, err = usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 2))
	noErr(t, err)
}

func TestIssueLabelWithAnUnknownOrderIsAValidationErrorAndStoresNothing(t *testing.T) {
	r := newRig(t)
	cmd := issueCmd("PO-1", 1)
	cmd.OrderID = "ord-unknown"
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), cmd)
	wantErr(t, err, shared.ErrValidation)
	if r.store.Counts().Labels != 0 {
		t.Fatal("nothing may be stored")
	}
}

func TestIssueLabelWhenCustomerOrderingIsDownStoresNothing(t *testing.T) {
	r := newRig(t)
	r.gateway.Fail(true)
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	wantErr(t, err, ports.ErrShipToUnavailable)
	if c := r.store.Counts(); c.Labels != 0 || c.Journeys != 0 || c.Snapshots != 0 || c.OutboxEvents != 0 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestIssueLabelRefusesAnIncompleteAddressAndASealerFailure(t *testing.T) {
	r := newRig(t)
	bad := sampleAddress()
	bad.City = ""
	r.gateway.Put("ord-1", bad)
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	wantErr(t, err, shared.ErrValidation)

	r.gateway.Put("ord-1", sampleAddress())
	boom := errors.New("kms down")
	r.deps.Sealer = memory.MarkSealer{Err: boom}
	_, err = usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	wantErr(t, err, boom)
	if r.store.Counts().Labels != 0 {
		t.Fatal("nothing may be stored")
	}
}

func TestIssueLabelRetriesOnceOnATrackingNumberCollision(t *testing.T) {
	r := newRig(t)
	r.issued(t) // takes RN000000000000001
	r.ids.Trackers = []shared.TrackingNumber{"RN000000000000001"}
	v, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-2", 1))
	noErr(t, err)
	if v.TrackingNumber == "RN000000000000001" || r.store.Counts().Labels != 2 {
		t.Fatalf("retry must mint a new number, got %s", v.TrackingNumber)
	}
}

func TestIssueLabelGivesUpAfterTwoCollisions(t *testing.T) {
	r := newRig(t)
	r.issued(t)
	r.ids.Trackers = []shared.TrackingNumber{"RN000000000000001", "RN000000000000001"}
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-2", 1))
	wantErr(t, err, label.ErrTrackingNumberTaken)
	if r.store.Counts().Labels != 1 || r.store.Counts().OutboxEvents != 1 {
		t.Fatalf("counts = %+v", r.store.Counts())
	}
}

func TestVoidLabelClosesLabelAndJourneyAndPublishes(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.clock.Advance(2 * time.Hour)
	v, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "order cancelled")
	noErr(t, err)
	if v.Status != "VOIDED" || v.VoidReason != "order cancelled" || v.VoidedAt == nil || !v.VoidedAt.Equal(hour(2)) {
		t.Fatalf("view = %+v", v)
	}
	j := r.journey(t, tn)
	if j.State() != journey.StateVoided || !j.ClosedAt().Equal(hour(2)) {
		t.Fatalf("journey = %s %v", j.State(), j.ClosedAt())
	}
	evs := r.store.Outbox().Events()
	if got := eventNames(evs); got != "LabelIssued,LabelVoided" {
		t.Fatalf("events = %s", got)
	}
}

func TestVoidLabelTwiceReturnsTheLabelUnchangedWithoutAnEvent(t *testing.T) {
	r := newRig(t)
	r.issued(t)
	_, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "first")
	noErr(t, err)
	before := r.store.Counts()
	v, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "second")
	noErr(t, err)
	if v.Status != "VOIDED" || v.VoidReason != "first" || r.store.Counts() != before {
		t.Fatalf("view=%+v counts=%+v", v, r.store.Counts())
	}
}

func TestVoidLabelAfterHandoverIsRefusedAndChangesNothing(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, journey.ScanPickupDeparture)
	before := r.store.Counts()
	_, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", "too late")
	wantErr(t, err, journey.ErrVoidAfterHandover)
	if r.store.Counts() != before {
		t.Fatal("a refused void must change nothing")
	}
}

func TestVoidLabelUnknownAndInvalidReason(t *testing.T) {
	r := newRig(t)
	_, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), "nope", "x")
	wantErr(t, err, ports.ErrNotFound)
	r.issued(t)
	before := r.store.Counts()
	_, err = usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-1", " ")
	wantErr(t, err, shared.ErrValidation)
	if r.store.Counts() != before {
		t.Fatal("an invalid reason must change nothing")
	}
}
