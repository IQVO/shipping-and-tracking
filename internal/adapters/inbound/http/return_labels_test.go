package http_test

import (
	"context"
	"strings"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// returnLabelBody is a valid POST /return-labels body for ord-1 (ADR 0017).
const returnLabelBody = `{"returnAuthorizationId":"ret-1","orderId":"ord-1","packageNo":1,"destinationPostalPrefix":"01310"}`

func TestIssueReturnLabelIssuesAReverseLabel(t *testing.T) {
	f := newFixture(t)
	r := f.do(t, "POST", "/return-labels", returnLabelBody)
	if r.status != 201 {
		t.Fatalf("status = %d, body %s", r.status, r.body)
	}
	for name, want := range map[string]string{
		"carrier": "RN-GROUND", "purpose": "RETURN", "returnAuthorizationId": "ret-1", "orderId": "ord-1",
	} {
		if got := r.field(t, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if strings.Contains(r.body, "poNumber") || strings.Contains(r.body, "assignmentId") {
		t.Errorf("a reverse label carries no poNumber or assignmentId: %s", r.body)
	}
	if r.field(t, "trackingNumber") == "" || r.field(t, "labelRef") == "" {
		t.Errorf("the response names the label: %s", r.body)
	}
	// A reverse label starts no journey: the tracking number is unknown.
	j := f.do(t, "GET", "/journeys/"+r.field(t, "trackingNumber"), "")
	if j.status != 404 {
		t.Fatalf("journey of a reverse label = %d, want 404", j.status)
	}
}

func TestIssueReturnLabelValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing authorization", `{"orderId":"ord-1","packageNo":1,"destinationPostalPrefix":"01310"}`},
		{"missing order", `{"returnAuthorizationId":"ret-1","packageNo":1,"destinationPostalPrefix":"01310"}`},
		{"missing prefix", `{"returnAuthorizationId":"ret-1","orderId":"ord-1","packageNo":1}`},
		{"no package", `{"returnAuthorizationId":"ret-1","orderId":"ord-1","destinationPostalPrefix":"01310"}`},
		{"bad prefix", `{"returnAuthorizationId":"ret-1","orderId":"ord-1","packageNo":1,"destinationPostalPrefix":"013 1"}`},
		{"unknown field", returnLabelBody[:len(returnLabelBody)-1] + `,"extra":1}`},
		{"unknown order", `{"returnAuthorizationId":"ret-1","orderId":"ord-9","packageNo":1,"destinationPostalPrefix":"01310"}`},
	}
	for _, c := range cases {
		f := newFixture(t)
		r := f.do(t, "POST", "/return-labels", c.body)
		if r.status != 422 || !strings.Contains(r.field(t, "type"), "validation-failed") {
			t.Errorf("%s: got %d %s", c.name, r.status, r.body)
		}
	}
}

func TestIssueReturnLabelSecondPackageOfOneAuthorizationIsAConflict(t *testing.T) {
	f := newFixture(t)
	first := f.do(t, "POST", "/return-labels", returnLabelBody)
	if first.status != 201 {
		t.Fatalf("first = %d %s", first.status, first.body)
	}
	again := f.do(t, "POST", "/return-labels", returnLabelBody)
	if again.status != 409 || !strings.Contains(again.body, "label-already-issued") {
		t.Fatalf("duplicate = %d %s, want 409 label-already-issued", again.status, again.body)
	}
	second := f.do(t, "POST", "/return-labels",
		`{"returnAuthorizationId":"ret-1","orderId":"ord-1","packageNo":2,"destinationPostalPrefix":"01310"}`)
	if second.status != 201 {
		t.Fatalf("package 2 = %d %s", second.status, second.body)
	}
	other := f.do(t, "POST", "/return-labels",
		`{"returnAuthorizationId":"ret-2","orderId":"ord-1","packageNo":1,"destinationPostalPrefix":"01310"}`)
	if other.status != 201 {
		t.Fatalf("other authorization = %d %s", other.status, other.body)
	}
}

func TestIssueReturnLabelMatchesTheOutboundStopKey(t *testing.T) {
	f := newFixture(t)
	out := f.do(t, "POST", "/labels",
		`{"poNumber":"PO-1","packageNo":1,"assignmentId":"asg-1","orderId":"ord-1","tier":"NEXT_DAY"}`)
	if out.status != 201 {
		t.Fatalf("outbound = %d %s", out.status, out.body)
	}
	rev := f.do(t, "POST", "/return-labels", returnLabelBody)
	if rev.status != 201 {
		t.Fatalf("reverse = %d %s", rev.status, rev.body)
	}
	// The same secret over the same ship-to yields the same stopKey: the
	// pickup stop groups with the delivery stop by construction (ADR 0017).
	if f.stopKey(t, out.field(t, "labelRef")) != f.stopKey(t, rev.field(t, "labelRef")) {
		t.Fatal("the reverse stopKey must equal the outbound stopKey of the same order")
	}
}

// stopKey reads a stored label's stop key.
func (f *fixture) stopKey(t *testing.T, ref string) string {
	t.Helper()
	l, err := f.store.Labels().Get(context.Background(), label.Ref(ref))
	if err != nil {
		t.Fatalf("load label %s: %v", ref, err)
	}
	return l.StopKey()
}
