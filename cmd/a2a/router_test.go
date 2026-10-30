package main

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

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	inbounda2a "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/a2a"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testCard is the card the binary would serve for testAgentURL.
func testCard() *a2a.AgentCard {
	return inbounda2a.Card(inbounda2a.Config{AgentURL: testAgentURL, Version: "test"})
}

// testHandler is the JSON-RPC handler over zero Deps: enough for the
// transport tests, where no skill reaches a database.
func testHandler() http.Handler {
	return inbounda2a.Handler(inbounda2a.NewExecutor(inbounda2a.Deps{}))
}

// newTestAgent builds the router exactly as run() does (newAgent), over an
// in-memory journey repository holding one journey. The memory adapter is a
// test double: it is imported by this _test file only, never by the binary
// (TestRuntimeBinaryHasNoInMemoryAdapters).
func newTestAgent(t *testing.T) http.Handler {
	t.Helper()
	journeys := memory.NewStore().Journeys()
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-1", PONumber: "PO-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
	}, time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := journeys.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	deps := inbounda2a.Deps{GetJourney: &usecases.GetJourney{Deps: usecases.Deps{Journeys: journeys}}}
	return newAgent(deps, testAgentURL, "test", quietLogger(), "shipping-and-tracking-a2a")
}

func TestRouter_HealthzIsOpenAndServesOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestAgent(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %q, want 200 {\"status\":\"ok\"}", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", ct)
	}
}

func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestAgent(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent card: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"shipping-and-tracking"`, testAgentURL, `"get_journey"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent card lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, "securitySchemes") {
		t.Errorf("the card must declare no security schemes: %s", body)
	}
}

// TestRouter_BothMountsReachTheJSONRPCHandler pins the fleet's
// <CTX>_A2A_ENDPOINT convention: the endpoint answers at "/" and at "/a2a",
// with no credentials, and the use case behind it is the wired one (the
// seeded journey is read).
func TestRouter_BothMountsReachTheJSONRPCHandler(t *testing.T) {
	h := newTestAgent(t)
	for _, path := range []string{"/", "/a2a"} {
		for _, auth := range []string{"", "Bearer nonsense"} {
			t.Run(path+" auth="+auth, func(t *testing.T) {
				body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"get_journey","arguments":{"trackingNumber":"RN0123456789ABCDEF"}}}]}}}`
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("A2A-Version", "1.0")
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				raw, _ := io.ReadAll(rec.Body)
				if rec.Code != http.StatusOK || !strings.Contains(string(raw), `"jsonrpc"`) {
					t.Fatalf("POST %s: %d %q does not look like a JSON-RPC reply", path, rec.Code, raw)
				}
				if !strings.Contains(string(raw), `\"state\":\"LABELLED\"`) && !strings.Contains(string(raw), `"state":"LABELLED"`) {
					t.Fatalf("POST %s: reply %q lacks the seeded journey read through the wired use case", path, raw)
				}
			})
		}
	}
}

// TestRouter_ACardFetchedFromTheBinaryIsDialable runs the discovery flow a
// delegating agent runs against the real router: fetch the card, build a
// client from it, call the skill.
func TestRouter_ACardFetchedFromTheBinaryIsDialable(t *testing.T) {
	srv := httptest.NewServer(newTestAgent(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	// The card advertises the configured URL, not the test server's: point the
	// client at the test server the way a gateway-rewritten route would.
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser,
		a2a.NewDataPart(map[string]any{"skill": "get_journey", "arguments": map[string]any{"trackingNumber": "RN0123456789ABCDEF"}}))})
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T", resp)
	}
	if text, _ := msg.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), "RN0123456789ABCDEF: LABELLED") {
		t.Fatalf("summary = %q, want the seeded journey", text)
	}
}
