package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var toOutForDelivery = append(append([]journey.ScanType{}, toAtStation...), journey.ScanOutForDelivery)

func etaCmd(id string, assignedHour int, tns ...shared.TrackingNumber) usecases.ApplyEtaWindowsCmd {
	return usecases.ApplyEtaWindowsCmd{
		Consumer: "last-mile-delivery", EventID: id, TrackingNumbers: tns,
		Start: hour(assignedHour + 1), End: hour(assignedHour + 3), AssignedAt: hour(assignedHour),
	}
}

func TestApplyEtaWindowsStoresThePublishesAndShowsTheWindow(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toOutForDelivery...)
	res, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(), etaCmd("ev-1", 4, tn))
	noErr(t, err)
	if res != (usecases.EtaResult{Applied: 1}) {
		t.Fatalf("result = %+v", res)
	}
	j := r.journey(t, tn)
	if !j.EtaStart().Equal(hour(5)) || !j.EtaEnd().Equal(hour(7)) || !j.EtaAssignedAt().Equal(hour(4)) || !j.EtaCurrent() {
		t.Fatalf("eta = %v..%v at %v current=%v", j.EtaStart(), j.EtaEnd(), j.EtaAssignedAt(), j.EtaCurrent())
	}
	names := eventNames(r.store.Outbox().Events())
	if names[len(names)-len("PackageEtaWindowAssigned"):] != "PackageEtaWindowAssigned" {
		t.Fatalf("events = %s", names)
	}
}

func TestApplyEtaWindowsHandlesEveryTrackingNumberOfTheStop(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	v2, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-2", 1))
	noErr(t, err)
	r.scanTo(t, tn, toOutForDelivery...)
	res, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(),
		etaCmd("ev-1", 4, tn, shared.TrackingNumber(v2.TrackingNumber), "RNNOPE"))
	noErr(t, err)
	if res != (usecases.EtaResult{Applied: 2, Unknown: 1}) {
		t.Fatalf("result = %+v", res)
	}
}

func TestApplyEtaWindowsSkipsAClaimedEventWithoutEffect(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toOutForDelivery...)
	cmd := etaCmd("ev-1", 4, tn)
	_, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(), cmd)
	noErr(t, err)
	before := r.store.Counts()
	res, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(), cmd)
	noErr(t, err)
	if !res.Skipped || res.Applied != 0 || r.store.Counts() != before {
		t.Fatalf("result=%+v counts=%+v", res, r.store.Counts())
	}
}

func TestApplyEtaWindowsCountsStaleAndClosedAndPublishesNothing(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	r.scanTo(t, tn, toOutForDelivery...)
	_, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(), etaCmd("ev-1", 5, tn))
	noErr(t, err)
	v2, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-2", 1))
	noErr(t, err)
	closed := shared.TrackingNumber(v2.TrackingNumber)
	_, err = usecases.NewVoidLabel(r.deps).Execute(context.Background(), "lbl-2", "x")
	noErr(t, err)
	events := r.store.Counts().OutboxEvents
	for i, c := range []struct {
		name string
		cmd  usecases.ApplyEtaWindowsCmd
		want usecases.EtaResult
	}{
		{"older", etaCmd("ev-2", 4, tn), usecases.EtaResult{Stale: 1}},
		{"same instant", etaCmd("ev-3", 5, tn), usecases.EtaResult{Stale: 1}},
		{"closed", etaCmd("ev-4", 6, closed), usecases.EtaResult{Closed: 1}},
	} {
		res, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(), c.cmd)
		noErr(t, err)
		if res != c.want {
			t.Errorf("%d %s: result = %+v, want %+v", i, c.name, res, c.want)
		}
	}
	if r.store.Counts().OutboxEvents != events {
		t.Fatal("a stale or closed window publishes nothing")
	}
}

func TestApplyEtaWindowsValidationFailureRollsBackTheClaim(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	bad := etaCmd("ev-1", 4, tn)
	bad.Start, bad.End = bad.End, bad.Start
	before := r.store.Counts()
	_, err := usecases.NewApplyEtaWindows(r.deps).Execute(context.Background(), bad)
	wantErr(t, err, shared.ErrValidation)
	if r.store.Counts() != before {
		t.Fatalf("the whole event must roll back, counts = %+v", r.store.Counts())
	}
	ok, err := r.store.Inbox().Claim(context.Background(), "last-mile-delivery", "ev-1")
	noErr(t, err)
	if !ok {
		t.Fatal("the claim must be free again")
	}
}
