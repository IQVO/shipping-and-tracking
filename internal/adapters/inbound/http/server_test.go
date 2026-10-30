package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/http"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

type fixture struct {
	srv     *httptest.Server
	store   *memory.Store
	gateway *memory.ShipToGateway
}

func newFixture(t *testing.T) *fixture { return newFixtureLogged(t, nil) }

// newFixtureLogged is newFixture with the router's request logger set (nil
// means slog.Default()).
func newFixtureLogged(t *testing.T, logger *slog.Logger) *fixture {
	t.Helper()
	s := memory.NewStore()
	gw := memory.NewShipToGateway()
	gw.Put("ord-1", shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", Region: "SP", PostalCode: "01310-100", CountryCode: "BR"})
	d := usecases.Deps{
		Labels: s.Labels(), Snapshots: s.Snapshots(), Journeys: s.Journeys(), Catalog: s.Catalog(), Inbox: s.Inbox(),
		Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{}, ShipTo: gw,
		Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
	}
	srv := httptest.NewServer(inbound.NewRouter(&inbound.Server{
		IssueLabel: usecases.NewIssueLabel(d), VoidLabel: usecases.NewVoidLabel(d),
		RecordScan: usecases.NewRecordScan(d), GetJourney: usecases.NewGetJourney(d),
	}, logger, ""))
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, store: s, gateway: gw}
}

type reply struct {
	status int
	ctype  string
	body   string
}

func (f *fixture) do(t *testing.T, method, path, body string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header.Get("Content-Type"), string(b)}
}

func (r reply) field(t *testing.T, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("body %q: %v", r.body, err)
	}
	s, _ := m[name].(string)
	return s
}

const issueBody = `{"poNumber":"PO-1","packageNo":1,"assignmentId":"asg-1","orderId":"ord-1","tier":"NEXT_DAY"}`

func scanBody(id, typ string, hours int) string {
	return `{"scanId":"` + id + `","nodeId":"n1","scanType":"` + typ + `","at":"` + t0.Add(time.Duration(hours)*time.Hour).Format(time.RFC3339) + `"}`
}

func TestIssueLabelThenReadTheJourney(t *testing.T) {
	f := newFixture(t)
	got := f.do(t, "POST", "/labels", issueBody)
	if got.status != 201 || got.ctype != "application/json" || got.field(t, "status") != "ISSUED" || got.field(t, "carrier") != "RN-EXPRESS" {
		t.Fatalf("issue = %+v", got)
	}
	tn := got.field(t, "trackingNumber")
	j := f.do(t, "GET", "/journeys/"+tn, "")
	if j.status != 200 || j.field(t, "state") != "LABELLED" {
		t.Fatalf("journey = %+v", j)
	}
	for _, leak := range []string{"Sample Street", "Ana Example", "Sampletown", "01310", "address", "shipTo"} {
		if strings.Contains(got.body+j.body, leak) {
			t.Fatalf("%q leaked into a response", leak)
		}
	}
}

func TestVoidLabelRouteWithTheColonSuffix(t *testing.T) {
	f := newFixture(t)
	ref := f.do(t, "POST", "/labels", issueBody).field(t, "labelRef")
	got := f.do(t, "POST", "/labels/"+ref+":void", `{"reason":"order cancelled"}`)
	if got.status != 200 || got.field(t, "status") != "VOIDED" {
		t.Fatalf("void = %+v", got)
	}
	again := f.do(t, "POST", "/labels/"+ref+":void", `{"reason":"again"}`)
	if again.status != 200 || again.field(t, "status") != "VOIDED" {
		t.Fatalf("second void = %+v", again)
	}
}

func TestRecordScanFlowAndItsRefusals(t *testing.T) {
	f := newFixture(t)
	tn := f.do(t, "POST", "/labels", issueBody).field(t, "trackingNumber")
	path := "/journeys/" + tn + "/scans"

	if got := f.do(t, "POST", path, scanBody("s1", "PICKUP_DEPARTURE", 1)); got.status != 200 || got.field(t, "state") != "HANDED_OVER" {
		t.Fatalf("scan = %+v", got)
	}
	cases := []struct {
		name, body, slug string
		status           int
	}{
		{"duplicate", scanBody("s1", "DEPARTURE", 2), "duplicate-scan", 409},
		{"out of order", scanBody("s0", "DEPARTURE", 0), "scan-out-of-order", 409},
		{"no edge", scanBody("s2", "DELIVERED", 3), "illegal-transition", 409},
	}
	for _, c := range cases {
		got := f.do(t, "POST", path, c.body)
		if got.status != c.status || got.ctype != "application/problem+json" || got.field(t, "type") != c.slug {
			t.Errorf("%s = %+v, want %d %s", c.name, got, c.status, c.slug)
		}
	}
	// A void after the handover is refused.
	ref := "lbl-1"
	if got := f.do(t, "POST", "/labels/"+ref+":void", `{"reason":"late"}`); got.status != 409 || got.field(t, "type") != "void-after-handover" {
		t.Errorf("void = %+v", got)
	}
}

func TestProblemDetailsOfTheErrorCatalogue(t *testing.T) {
	f := newFixture(t)
	f.do(t, "POST", "/labels", issueBody)
	f.gateway.Fail(true)
	unavailable := f.do(t, "POST", "/labels", strings.Replace(issueBody, `"packageNo":1`, `"packageNo":2`, 1))
	f.gateway.Fail(false)

	cases := []struct {
		name   string
		got    reply
		status int
		slug   string
	}{
		{"already issued", f.do(t, "POST", "/labels", issueBody), 409, "label-already-issued"},
		{"unknown order", f.do(t, "POST", "/labels", strings.Replace(strings.Replace(issueBody, "ord-1", "ord-9", 1), "PO-1", "PO-9", 1)), 422, "validation-failed"},
		{"ship-to down", unavailable, 503, "ship-to-unavailable"},
		{"bad tier", f.do(t, "POST", "/labels", strings.Replace(issueBody, "NEXT_DAY", "WEEKLY", 1)), 422, "validation-failed"},
		{"package zero", f.do(t, "POST", "/labels", strings.Replace(issueBody, `"packageNo":1`, `"packageNo":0`, 1)), 422, "validation-failed"},
		{"missing field", f.do(t, "POST", "/labels", `{"poNumber":"PO-1"}`), 422, "validation-failed"},
		{"too long", f.do(t, "POST", "/labels", strings.Replace(issueBody, "PO-1", strings.Repeat("x", 65), 1)), 422, "validation-failed"},
		{"unknown field", f.do(t, "POST", "/labels", strings.TrimSuffix(issueBody, "}")+`,"address":"x"}`), 422, "validation-failed"},
		{"not json", f.do(t, "POST", "/labels", `nope`), 422, "validation-failed"},
		{"empty body", f.do(t, "POST", "/labels", ``), 422, "validation-failed"},
		{"trailing data", f.do(t, "POST", "/labels", issueBody+`{}`), 422, "validation-failed"},
		{"void unknown", f.do(t, "POST", "/labels/nope:void", `{"reason":"x"}`), 404, "not-found"},
		{"void no reason", f.do(t, "POST", "/labels/lbl-1:void", `{}`), 422, "validation-failed"},
		{"journey unknown", f.do(t, "GET", "/journeys/nope", ""), 404, "not-found"},
		{"scan unknown journey", f.do(t, "POST", "/journeys/nope/scans", scanBody("s", "DEPARTURE", 1)), 404, "not-found"},
		{"scan bad type", f.do(t, "POST", "/journeys/x/scans", scanBody("s", "TELEPORT", 1)), 422, "validation-failed"},
		{"scan no time", f.do(t, "POST", "/journeys/x/scans", `{"scanId":"s","nodeId":"n","scanType":"DEPARTURE"}`), 422, "validation-failed"},
		{"unknown path", f.do(t, "GET", "/nope", ""), 404, "not-found"},
		{"wrong method", f.do(t, "DELETE", "/labels", ""), 405, "method-not-allowed"},
	}
	for _, c := range cases {
		if c.got.status != c.status || c.got.ctype != "application/problem+json" || c.got.field(t, "type") != c.slug {
			t.Errorf("%s = %+v, want %d %s", c.name, c.got, c.status, c.slug)
			continue
		}
		var p struct {
			Title    string `json:"title"`
			Status   int    `json:"status"`
			Instance string `json:"instance"`
		}
		_ = json.Unmarshal([]byte(c.got.body), &p)
		if p.Title == "" || p.Status != c.status || p.Instance == "" {
			t.Errorf("%s: incomplete problem %s", c.name, c.got.body)
		}
	}
}

func TestNULCharactersAreClientErrors(t *testing.T) {
	f := newFixture(t)
	for name, got := range map[string]reply{
		"body raw":     f.do(t, "POST", "/labels", strings.Replace(issueBody, "PO-1", "PO\x00-1", 1)),
		"body escaped": f.do(t, "POST", "/labels", strings.Replace(issueBody, "PO-1", `PO\u0000-1`, 1)),
		"query":        f.do(t, "GET", "/journeys/x?a=%00", ""),
	} {
		if got.status != 422 || got.field(t, "type") != "validation-failed" {
			t.Errorf("%s = %+v", name, got)
		}
	}
	if got := f.do(t, "GET", "/journeys/x%00y", ""); got.status != 404 || got.field(t, "type") != "not-found" {
		t.Errorf("path = %+v", got)
	}
}

func TestHealthzAndReadyz(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/healthz", "/readyz"} {
		if got := f.do(t, "GET", p, ""); got.status != 200 {
			t.Errorf("%s = %+v", p, got)
		}
	}
}
