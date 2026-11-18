package views_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

func newLabel(t *testing.T) *label.ShippingLabel {
	t.Helper()
	l, _, err := label.Issue(label.NewLabel{
		Ref: "lbl-1", TrackingNumber: "RN1", PONumber: "PO-1", PackageNo: 1, AssignmentID: "asg-1", OrderID: "ord-1",
		Tier: shared.TierTwoDay, StopKey: strings.Repeat("a", label.StopKeyLen), DestinationPostalPrefix: "01310",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLabelViewIssuedAndVoidedJSON(t *testing.T) {
	l := newLabel(t)
	b, _ := json.Marshal(views.LabelFrom(l))
	want := `{"labelRef":"lbl-1","trackingNumber":"RN1","carrier":"RN-GROUND","poNumber":"PO-1","packageNo":1,"assignmentId":"asg-1","orderId":"ord-1","tier":"TWO_DAY","status":"ISSUED","issuedAt":"2026-10-23T08:00:00Z"}`
	if string(b) != want {
		t.Fatalf("issued JSON:\n%s\nwant\n%s", b, want)
	}
	if _, err := l.Void("gone", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(views.LabelFrom(l))
	want = `{"labelRef":"lbl-1","trackingNumber":"RN1","carrier":"RN-GROUND","poNumber":"PO-1","packageNo":1,"assignmentId":"asg-1","orderId":"ord-1","tier":"TWO_DAY","status":"VOIDED","issuedAt":"2026-10-23T08:00:00Z","voidReason":"gone","voidedAt":"2026-10-23T09:00:00Z"}`
	if string(b) != want {
		t.Fatalf("voided JSON:\n%s\nwant\n%s", b, want)
	}
	if strings.Contains(string(b), "stopKey") {
		t.Fatal("the stopKey is not part of the REST label")
	}
}

func newReturnLabel(t *testing.T) *label.ShippingLabel {
	t.Helper()
	l, _, err := label.Issue(label.NewLabel{
		Ref: "lbl-ret-1", TrackingNumber: "RNRET1", PackageNo: 1, OrderID: "ord-1",
		Purpose: label.PurposeReturn, ReturnAuthorizationID: "ret-1",
		Tier: shared.TierStandard, StopKey: strings.Repeat("a", label.StopKeyLen), DestinationPostalPrefix: "01310",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// The forward label contract pins poNumber and assignmentId as required
// (apis/openapi.yaml, Label): the view type cannot omit them, whatever it holds.
func TestLabelViewAlwaysCarriesPONumberAndAssignmentID(t *testing.T) {
	b, _ := json.Marshal(views.Label{})
	for _, key := range []string{`"poNumber":""`, `"assignmentId":""`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("an outbound label view must always carry %s: %s", key, b)
		}
	}
	for _, key := range []string{"purpose", "returnAuthorizationId"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("an outbound label view never carries %s: %s", key, b)
		}
	}
}

// A reverse label has its own schema (apis/openapi.yaml, ReturnLabel): never a
// poNumber or an assignmentId, always purpose RETURN and the authorization.
func TestReturnLabelViewIssuedAndVoidedJSON(t *testing.T) {
	l := newReturnLabel(t)
	b, _ := json.Marshal(views.ReturnLabelFrom(l))
	want := `{"labelRef":"lbl-ret-1","trackingNumber":"RNRET1","carrier":"RN-GROUND","packageNo":1,"orderId":"ord-1","tier":"STANDARD","status":"ISSUED","issuedAt":"2026-10-23T08:00:00Z","purpose":"RETURN","returnAuthorizationId":"ret-1"}`
	if string(b) != want {
		t.Fatalf("issued JSON:\n%s\nwant\n%s", b, want)
	}
	if _, err := l.Void("never shipped", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(views.ReturnLabelFrom(l))
	want = `{"labelRef":"lbl-ret-1","trackingNumber":"RNRET1","carrier":"RN-GROUND","packageNo":1,"orderId":"ord-1","tier":"STANDARD","status":"VOIDED","issuedAt":"2026-10-23T08:00:00Z","voidReason":"never shipped","voidedAt":"2026-10-23T09:00:00Z","purpose":"RETURN","returnAuthorizationId":"ret-1"}`
	if string(b) != want {
		t.Fatalf("voided JSON:\n%s\nwant\n%s", b, want)
	}
	// Even an empty view cannot grow the outbound-only keys.
	b, _ = json.Marshal(views.ReturnLabel{})
	for _, key := range []string{"poNumber", "assignmentId", "stopKey"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("a reverse label view never carries %s: %s", key, b)
		}
	}
}

// A route that serves either kind (the void route) answers the view of the
// label's purpose: the oneOf of apis/openapi.yaml.
func TestAnyLabelFromPicksTheViewOfThePurpose(t *testing.T) {
	out, ok := views.AnyLabelFrom(newLabel(t)).(views.Label)
	if !ok || out.PONumber != "PO-1" || out.AssignmentID != "asg-1" {
		t.Fatalf("an outbound label answers views.Label, got %#v", views.AnyLabelFrom(newLabel(t)))
	}
	rev, ok := views.AnyLabelFrom(newReturnLabel(t)).(views.ReturnLabel)
	if !ok || rev.Purpose != "RETURN" || rev.ReturnAuthorizationID != "ret-1" {
		t.Fatalf("a reverse label answers views.ReturnLabel, got %#v", views.AnyLabelFrom(newReturnLabel(t)))
	}
}

func TestJourneyViewJSONOpenAndDelivered(t *testing.T) {
	j, err := journey.Start(journey.NewJourney{TrackingNumber: "RN1", LabelRef: "lbl-1", PONumber: "PO-1", AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierTwoDay}, t0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(views.JourneyFrom(j))
	want := `{"trackingNumber":"RN1","state":"LABELLED","poNumber":"PO-1","assignmentId":"asg-1","orderId":"ord-1","tier":"TWO_DAY","scans":[]}`
	if string(b) != want {
		t.Fatalf("open JSON:\n%s\nwant\n%s", b, want)
	}
	for i, typ := range []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery, journey.ScanDelivered} {
		at := t0.Add(time.Duration(i+1) * time.Hour)
		if _, err := j.RecordScan(journey.ScanInput{ID: "s" + string(rune('a'+i)), NodeID: "n1", Type: typ, At: at}, at); err != nil {
			t.Fatal(err)
		}
	}
	v := views.JourneyFrom(j)
	if v.State != "DELIVERED" || v.HandedOverAt == nil || v.DeliveredAt == nil || len(v.Scans) != 5 || v.Scans[4].ScanType != "DELIVERED" || !v.Scans[4].Applied {
		t.Fatalf("view = %+v", v)
	}
	b, _ = json.Marshal(v.Scans[0])
	if string(b) != `{"scanId":"sa","nodeId":"n1","scanType":"PICKUP_DEPARTURE","at":"2026-10-23T09:00:00Z","applied":true}` {
		t.Fatalf("scan JSON = %s", b)
	}
}
