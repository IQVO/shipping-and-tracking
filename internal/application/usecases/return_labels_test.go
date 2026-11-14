package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func returnCmd(auth string, pkg int) usecases.IssueReturnLabelCmd {
	return usecases.IssueReturnLabelCmd{
		ReturnAuthorizationID: auth, OrderID: "ord-1", PackageNo: pkg, DestinationPostalPrefix: "01310",
	}
}

func TestIssueReturnLabelIssuesWithoutJourneyOrSnapshot(t *testing.T) {
	r := newRig(t)
	v, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), returnCmd("ret-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if v.Carrier != label.CarrierGround || v.Purpose != "RETURN" || v.ReturnAuthorizationID != "ret-1" || v.PONumber != "" {
		t.Fatalf("view = %+v", v)
	}
	// No journey: the (poNumber, packageNo) identity does not fit (ADR 0017).
	if _, err := r.store.Journeys().Get(context.Background(), shared.TrackingNumber(v.TrackingNumber)); err == nil {
		t.Fatal("a reverse label must not start a PackageJourney")
	}
	// No ship-to snapshot: retention rides the journey this label lacks.
	if has, err := r.store.Snapshots().Has(context.Background(), label.Ref(v.LabelRef)); err != nil || has {
		t.Fatalf("snapshot stored: %v %v", has, err)
	}
	// LabelIssued carries purpose and returnAuthorizationId.
	evs := r.store.Outbox().Events()
	if len(evs) != 1 {
		t.Fatalf("outbox has %d events, want 1", len(evs))
	}
	issued, ok := evs[0].(label.LabelIssued)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	if issued.Purpose != label.PurposeReturn || issued.ReturnAuthorizationID != "ret-1" {
		t.Fatalf("event purpose = %s/%s", issued.Purpose, issued.ReturnAuthorizationID)
	}
	if issued.OrderID != "ord-1" || issued.Carrier != label.CarrierGround {
		t.Fatalf("event = %+v", issued)
	}
}

func TestIssueReturnLabelRepeatsTheOutboundStopKey(t *testing.T) {
	r := newRig(t)
	out, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), returnCmd("ret-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	outLabel, err := r.store.Labels().Get(context.Background(), label.Ref(out.LabelRef))
	if err != nil {
		t.Fatal(err)
	}
	revLabel, err := r.store.Labels().Get(context.Background(), label.Ref(rev.LabelRef))
	if err != nil {
		t.Fatal(err)
	}
	if outLabel.StopKey() != revLabel.StopKey() {
		t.Fatalf("reverse stopKey %s must equal the outbound %s", revLabel.StopKey(), outLabel.StopKey())
	}
	if revLabel.DestinationPostalPrefix() != "01310" {
		t.Fatalf("the label prefix is the return destination's: %s", revLabel.DestinationPostalPrefix())
	}
}

func TestIssueReturnLabelOnePerAuthorizationAndPackage(t *testing.T) {
	r := newRig(t)
	issue := usecases.NewIssueReturnLabel(r.deps)
	if _, err := issue.Execute(context.Background(), returnCmd("ret-1", 1)); err != nil {
		t.Fatal(err)
	}
	_, err := issue.Execute(context.Background(), returnCmd("ret-1", 1))
	if !errors.Is(err, label.ErrLabelAlreadyIssued) {
		t.Fatalf("duplicate must be label-already-issued, got %v", err)
	}
	if _, err := issue.Execute(context.Background(), returnCmd("ret-1", 2)); err != nil {
		t.Fatalf("package 2: %v", err)
	}
	if _, err := issue.Execute(context.Background(), returnCmd("ret-2", 1)); err != nil {
		t.Fatalf("another authorization: %v", err)
	}
}

func TestVoidReturnLabelNeedsNoJourney(t *testing.T) {
	r := newRig(t)
	v, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), returnCmd("ret-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := usecases.NewVoidLabel(r.deps).Execute(context.Background(), label.Ref(v.LabelRef), "return expired")
	if err != nil {
		t.Fatalf("void a reverse label: %v", err)
	}
	if got.Status != "VOIDED" || got.Purpose != "RETURN" {
		t.Fatalf("voided view = %+v", got)
	}
}

func TestIssueReturnLabelUnknownOrderIsValidation(t *testing.T) {
	r := newRig(t)
	cmd := returnCmd("ret-1", 1)
	cmd.OrderID = "ord-9"
	_, err := usecases.NewIssueReturnLabel(r.deps).Execute(context.Background(), cmd)
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("err = %v, want validation", err)
	}
}
