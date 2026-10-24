package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/mcp"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// t0 is the fixture's fixed "now".
var t0 = time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)

// harness is a connected in-memory MCP client over the real server wired to
// the in-memory repositories, exactly as cmd/mcp wires them (minus Postgres).
// The write side exists ONLY here, to seed a journey the way the REST adapter
// and the consumers would: the MCP surface itself has none.
type harness struct {
	session *sdk.ClientSession
	store   *memory.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	deps := inboundmcp.Deps{
		GetJourney: &usecases.GetJourney{Deps: usecases.Deps{Journeys: store.Journeys()}},
	}
	return &harness{session: connectSession(t, deps), store: store}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// saveJourney stores j through the repository, keeping the persistence
// contract (every save advances the version by exactly one).
func (h *harness) saveJourney(t *testing.T, j *journey.Journey) {
	t.Helper()
	must(t, h.store.Journeys().Save(t.Context(), j))
}

// seedJourney stores one journey of RN0123456789ABCDEF driven to IN_TRANSIT:
// a PICKUP_DEPARTURE scan at node-A (applied, LABELLED -> HANDED_OVER) then
// a DEPARTURE scan at node-B (applied, HANDED_OVER -> IN_TRANSIT), plus one
// out-of-order scan stored but never applied. This is the state the write
// side (REST scans and the consumed events) leaves behind; the read surface
// must return it verbatim.
func seedJourney(t *testing.T, h *harness) {
	t.Helper()
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: "RN0123456789ABCDEF", LabelRef: "lbl-1", PONumber: "PO-1",
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
	}, t0)
	must(t, err)
	h.saveJourney(t, j)
	_, err = j.RecordScan(journey.ScanInput{ID: "scan-1", NodeID: "node-A", Type: journey.ScanPickupDeparture, At: t0.Add(time.Hour)}, t0.Add(time.Hour))
	must(t, err)
	h.saveJourney(t, j)
	_, err = j.RecordScan(journey.ScanInput{ID: "scan-2", NodeID: "node-B", Type: journey.ScanDeparture, At: t0.Add(2 * time.Hour)}, t0.Add(2*time.Hour))
	must(t, err)
	h.saveJourney(t, j)
	// An older scan after a newer applied one: stored with applied=false and
	// ErrScanOutOfOrder (Result.Changed), exactly as the REST adapter keeps it.
	_, err = j.RecordScan(journey.ScanInput{ID: "scan-3", NodeID: "node-C", Type: journey.ScanTransitArrival, At: t0.Add(30 * time.Minute)}, t0.Add(3*time.Hour))
	if !errors.Is(err, journey.ErrScanOutOfOrder) {
		t.Fatalf("the out-of-order scan = %v, want ErrScanOutOfOrder", err)
	}
	h.saveJourney(t, j)
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
