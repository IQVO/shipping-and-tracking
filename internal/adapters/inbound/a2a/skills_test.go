package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// t0 is the fixture's fixed "now".
var t0 = time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)

const seededTracking = "RN0123456789ABCDEF"

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// seededJourneys holds one journey driven to IN_TRANSIT (two applied scans and
// one out-of-order scan stored but never applied: the state the write side
// leaves behind) and one fresh LABELLED journey with no scan.
func seededJourneys(t *testing.T) ports.JourneyRepository {
	t.Helper()
	repo := memory.NewStore().Journeys()
	save := func(j *journey.Journey) { must(t, repo.Save(t.Context(), j)) }

	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: seededTracking, LabelRef: "lbl-1", PONumber: "PO-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
	}, t0)
	must(t, err)
	save(j)
	_, err = j.RecordScan(journey.ScanInput{ID: "scan-1", NodeID: "node-A", Type: journey.ScanPickupDeparture, At: t0.Add(time.Hour)}, t0.Add(time.Hour))
	must(t, err)
	save(j)
	_, err = j.RecordScan(journey.ScanInput{ID: "scan-2", NodeID: "node-B", Type: journey.ScanDeparture, At: t0.Add(2 * time.Hour)}, t0.Add(2*time.Hour))
	must(t, err)
	save(j)
	_, err = j.RecordScan(journey.ScanInput{ID: "scan-3", NodeID: "node-C", Type: journey.ScanTransitArrival, At: t0.Add(30 * time.Minute)}, t0.Add(3*time.Hour))
	if !errors.Is(err, journey.ErrScanOutOfOrder) {
		t.Fatalf("the out-of-order scan = %v, want ErrScanOutOfOrder", err)
	}
	save(j)

	fresh, err := journey.Start(journey.NewJourney{
		TrackingNumber: "RN0000000000000001", LabelRef: "lbl-2", PONumber: "PO-2",
		AssignmentID: "asg-2", OrderID: "ord-2", Tier: shared.TierStandard,
	}, t0)
	must(t, err)
	save(fresh)
	return repo
}

func depsOver(repo ports.JourneyRepository) Deps {
	return Deps{GetJourney: &usecases.GetJourney{Deps: usecases.Deps{Journeys: repo}}}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		Journey *wireJourney `json:"journey"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireJourney struct {
	TrackingNumber string     `json:"trackingNumber"`
	State          string     `json:"state"`
	PONumber       string     `json:"poNumber"`
	AssignmentID   string     `json:"assignmentId"`
	OrderID        string     `json:"orderId"`
	Tier           string     `json:"tier"`
	HandedOverAt   string     `json:"handedOverAt"`
	DeliveredAt    string     `json:"deliveredAt"`
	Scans          []wireScan `json:"scans"`
}

type wireScan struct {
	ScanID   string `json:"scanId"`
	NodeID   string `json:"nodeId"`
	ScanType string `json:"scanType"`
	At       string `json:"at"`
	Applied  bool   `json:"applied"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope, its raw JSON
// and the one-line text summary of the agent's reply. A JSON-RPC level error
// fails the test: every outcome of a skill is answered in-band.
func (w *wire) call(t *testing.T, part *a2a.Part) (env wireEnvelope, raw, text string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage returned a transport error (want an in-band answer): %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	b, _ := json.Marshal(data.Value)
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, b)
	}
	summary, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(b), string(summary)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func (w *wire) track(t *testing.T, trackingNumber any) (wireEnvelope, string, string) {
	t.Helper()
	return w.skill(t, skillGetJourney, map[string]any{"trackingNumber": trackingNumber})
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func TestWireGetJourneyReturnsTheTimeline(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))

	env, _, text := w.track(t, seededTracking)
	j := env.Data.Journey
	if env.Problem != nil || j == nil || env.Skill != skillGetJourney {
		t.Fatalf("envelope = %+v", env)
	}
	assertSeededHeader(t, j)
	assertSeededScans(t, j.Scans)
	if text != seededTracking+": IN_TRANSIT, 3 scans (2 applied)" {
		t.Fatalf("summary = %q", text)
	}
}

func assertSeededHeader(t *testing.T, j *wireJourney) {
	t.Helper()
	if j.TrackingNumber != seededTracking || j.State != "IN_TRANSIT" || j.PONumber != "PO-1" || j.AssignmentID != "asg-1" ||
		j.OrderID != "ord-1" || j.Tier != "NEXT_DAY" || j.HandedOverAt != "2026-10-24T09:00:00Z" || j.DeliveredAt != "" {
		t.Fatalf("journey = %+v", j)
	}
}

func assertSeededScans(t *testing.T, scans []wireScan) {
	t.Helper()
	if len(scans) != 3 {
		t.Fatalf("scans = %+v, want 3 (applied or not)", scans)
	}
	if first := scans[0]; first != (wireScan{"scan-1", "node-A", "PICKUP_DEPARTURE", "2026-10-24T09:00:00Z", true}) {
		t.Fatalf("first scan = %+v", first)
	}
	if third := scans[2]; third.ScanID != "scan-3" || third.Applied {
		t.Fatalf("the out-of-order scan must be stored with applied=false: %+v", third)
	}
}

// A journey with no scan yet is still readable: LABELLED, an empty (not null)
// scan list, no handover or delivery instant.
func TestWireGetJourneyLabelledWithNoScan(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))

	env, raw, text := w.track(t, "RN0000000000000001")
	j := env.Data.Journey
	if env.Problem != nil || j == nil || j.State != "LABELLED" || j.Tier != "STANDARD" || j.Scans == nil || len(j.Scans) != 0 {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.Contains(raw, `"scans":[]`) || strings.Contains(raw, "handedOverAt") || strings.Contains(raw, "deliveredAt") {
		t.Fatalf("raw journey = %s, want an empty scans array and no omitted instants", raw)
	}
	if text != "RN0000000000000001: LABELLED, 0 scans (0 applied)" {
		t.Fatalf("summary = %q", text)
	}
}

// The tracking number is trimmed like the REST path parameter is.
func TestWireGetJourneyTrimsTheTrackingNumber(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))
	env, _, _ := w.track(t, "  "+seededTracking+"\n")
	if env.Problem != nil || env.Data.Journey == nil || env.Data.Journey.TrackingNumber != seededTracking {
		t.Fatalf("envelope = %+v", env)
	}
}

// The journey carries no ship-to address, by construction of the view; the
// answer must not carry one either, in any spelling.
func TestWireAnswersCarryNoShipToAddress(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))
	_, raw, text := w.track(t, seededTracking)
	for _, banned := range []string{"address", "shipto", "ship_to", "street", "postal", "postcode", "zip"} {
		if strings.Contains(strings.ToLower(raw+text), banned) {
			t.Errorf("the answer mentions %q: %s | %s", banned, raw, text)
		}
	}
}

// An unknown tracking number is a 404 answered in-band with the same slug and
// status the REST adapter gives GET /journeys/{trackingNumber}.
func TestWireUnknownJourneyIsAnsweredAs404NotRaised(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))

	env, _, text := w.track(t, "RN9999999999999999")
	wantProblem(t, env, http.StatusNotFound, "not-found")
	if env.Problem.Title != "Resource not found" || env.Problem.Detail != `no journey exists for tracking number "RN9999999999999999"` {
		t.Fatalf("problem = %+v", env.Problem)
	}
	// The shared error text carries internal wording ("not found: journey ...");
	// the adapter owns the answer text and must not leak it.
	for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
		if strings.Contains(s, "not found:") || strings.Contains(s, "ports") {
			t.Fatalf("an answer leaks the internal error text: %q", s)
		}
	}
}

// countingRepo counts reads, so a test can prove a value never reached the
// database.
type countingRepo struct {
	ports.JourneyRepository
	gets atomic.Int32
}

func (c *countingRepo) Get(ctx context.Context, tn shared.TrackingNumber) (*journey.Journey, error) {
	c.gets.Add(1)
	return c.JourneyRepository.Get(ctx, tn)
}

// A value that can never be a tracking number (a NUL character, inner
// whitespace, an over-long id, a character outside the id alphabet) is the 404
// REST answers for the same value in the path, and never reaches the
// repository: a NUL there would be a Postgres SQLSTATE 22021, a 500.
func TestWireMalformedTrackingNumberIs404WithoutAQuery(t *testing.T) {
	repo := &countingRepo{JourneyRepository: seededJourneys(t)}
	w := newWire(t, depsOver(repo))

	for name, tn := range map[string]string{
		"NUL":          "RN01\x00",
		"inner space":  "RN 01",
		"too long":     strings.Repeat("A", shared.MaxID+1),
		"slash":        "RN/01",
		"leading dash": "-RN01",
	} {
		t.Run(name, func(t *testing.T) {
			env, raw, _ := w.track(t, tn)
			wantProblem(t, env, http.StatusNotFound, "not-found")
			if strings.Contains(raw, "A") && len(raw) > 4*shared.MaxID {
				t.Fatalf("the answer echoes an over-long value: %d bytes", len(raw))
			}
		})
	}
	if n := repo.gets.Load(); n != 0 {
		t.Fatalf("a malformed tracking number reached the repository %d times", n)
	}
	// Sanity: a well-formed one does reach it.
	w.track(t, seededTracking)
	if repo.gets.Load() != 1 {
		t.Fatalf("a well-formed tracking number must be read, gets = %d", repo.gets.Load())
	}
}

// failingRepo makes every read fail, standing in for a database outage.
type failingRepo struct {
	ports.JourneyRepository
	err error
}

func (f failingRepo) Get(context.Context, shared.TrackingNumber) (*journey.Journey, error) {
	return nil, f.err
}

// An unexpected failure is answered as a 500 problem, never a JSON-RPC error,
// and its raw text (which may carry infrastructure detail) is not echoed. A
// value-object rejection from the use case is the REST 422.
func TestWireUseCaseFailuresAreClassifiedLikeREST(t *testing.T) {
	boom := errors.New("pgx: connection refused to 10.0.0.7:5432 password=hunter2")
	cases := map[string]struct {
		err    error
		status int
		slug   string
	}{
		"unexpected":        {boom, http.StatusInternalServerError, "internal-error"},
		"validation":        {shared.Invalid("tracking number %s", "x"), http.StatusUnprocessableEntity, "validation-failed"},
		"wrapped not found": {errors.Join(boom, ports.ErrNotFound), http.StatusNotFound, "not-found"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWire(t, depsOver(failingRepo{JourneyRepository: memory.NewStore().Journeys(), err: tc.err}))
			env, raw, text := w.track(t, seededTracking)
			wantProblem(t, env, tc.status, tc.slug)
			for _, s := range []string{raw, text} {
				if strings.Contains(s, "hunter2") || strings.Contains(s, "10.0.0.7") || strings.Contains(s, "pgx") {
					t.Fatalf("an answer leaks the underlying error: %q", s)
				}
			}
		})
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))
	cases := map[string]map[string]any{
		"missing trackingNumber":    {},
		"empty trackingNumber":      {"trackingNumber": ""},
		"blank trackingNumber":      {"trackingNumber": "   "},
		"trackingNumber wrong type": {"trackingNumber": 7},
		"trackingNumber null-ish":   {"trackingNumber": []string{"RN1"}},
		"unknown argument":          {"trackingNumber": seededTracking, "includeAddress": true},
		"typo'd argument":           {"trackingNumbre": seededTracking},
		"label argument":            {"labelRef": "lbl-1"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			env, _, _ := w.skill(t, skillGetJourney, args)
			wantProblem(t, env, http.StatusBadRequest, "invalid-argument")
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	w := newWire(t, depsOver(seededJourneys(t)))

	for name, part := range map[string]*a2a.Part{
		"not json":                a2a.NewTextPart("not json at all"),
		"no skill field":          a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}),
		"unknown request field":   a2a.NewDataPart(map[string]any{"skill": skillGetJourney, "arguments": map[string]any{"trackingNumber": seededTracking}, "extra": 1}),
		"trailing data":           a2a.NewTextPart(`{"skill":"get_journey","arguments":{"trackingNumber":"` + seededTracking + `"}} {"skill":"get_journey"}`),
		"arguments not an object": a2a.NewDataPart(map[string]any{"skill": skillGetJourney, "arguments": "RN1"}),
		"raw bytes part":          a2a.NewRawPart([]byte(`{"skill":"get_journey"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			env, _, _ := w.call(t, part)
			if env.Problem == nil || env.Problem.Status != http.StatusBadRequest {
				t.Fatalf("envelope = %+v, want an in-band 400", env)
			}
		})
	}

	env, _, _ := w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, http.StatusBadRequest, "unknown-skill")

	// A text part holding the same JSON is accepted (documented contract).
	env, _, _ = w.call(t, a2a.NewTextPart(`{"skill":"get_journey","arguments":{"trackingNumber":"`+seededTracking+`"}}`))
	if env.Problem != nil || env.Data.Journey == nil || env.Data.Journey.TrackingNumber != seededTracking {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}
}

// The write operations of the REST surface are not offered here: asking for
// one is an unknown skill, answered in-band, and nothing is written.
func TestWireOffersNoWriteSkill(t *testing.T) {
	repo := seededJourneys(t)
	w := newWire(t, depsOver(repo))

	for _, skill := range []string{"issue_label", "void_label", "record_scan", "apply_scan", "return_to_sender"} {
		env, _, _ := w.skill(t, skill, map[string]any{"trackingNumber": seededTracking, "scanId": "scan-x"})
		wantProblem(t, env, http.StatusBadRequest, "unknown-skill")
	}
	j, err := repo.Get(t.Context(), seededTracking)
	must(t, err)
	if len(j.Scans()) != 3 {
		t.Fatalf("a write skill name must not write anything, scans = %d", len(j.Scans()))
	}
}

// Each skill run opens an OTel span carrying the a2a.skill attribute, like the
// MCP adapter's mcp.tool span.
func TestSkillRunOpensASpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	w := newWire(t, depsOver(seededJourneys(t)))
	w.track(t, seededTracking)

	for _, s := range rec.Ended() {
		if s.Name() != "a2a.skill "+skillGetJourney {
			continue
		}
		for _, a := range s.Attributes() {
			if string(a.Key) == "a2a.skill" && a.Value.AsString() == skillGetJourney {
				return
			}
		}
		t.Fatalf("span %q lacks the a2a.skill attribute: %v", s.Name(), s.Attributes())
	}
	t.Fatal("no a2a.skill span was recorded")
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(depsOver(seededJourneys(t)))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	env, _, _ := (&wire{client: client}).track(t, seededTracking)
	if env.Data.Journey == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
