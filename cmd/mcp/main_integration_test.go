//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/mcp"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases
// are already migrated, a second run must be a no-op), then reads through
// the MCP tool exactly what the write side persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheApiWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	seedJourneyInPostgres(t, ctx, url)

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	before := countOutbox()

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "get_journey", Arguments: map[string]any{"trackingNumber": "RN0123456789ABCDEF"},
	})
	if err != nil || res.IsError {
		t.Fatalf("get_journey: err=%v res=%+v", err, res)
	}
	j, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("structured content = %#v, want an object", res.StructuredContent)
	}
	if j["state"] != "IN_TRANSIT" || j["poNumber"] != "PO-1" || j["handedOverAt"] != "2026-10-24T09:00:00Z" {
		t.Fatalf("get_journey = %v", j)
	}
	scans, _ := j["scans"].([]any)
	if len(scans) != 2 {
		t.Fatalf("scans = %v, want 2", j["scans"])
	}

	// An unknown tracking number is a not-found tool error, not a transport one.
	res, err = session.CallTool(ctx, &sdk.CallToolParams{
		Name: "get_journey", Arguments: map[string]any{"trackingNumber": "RN0000000000000000"},
	})
	if err != nil {
		t.Fatalf("unknown tracking number: transport error %v", err)
	}
	if !res.IsError {
		t.Fatalf("unknown tracking number: want an isError tool result, got %#v", res.StructuredContent)
	}

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// seedJourneyInPostgres stores one journey the way the write side leaves it
// (the aggregates over the real repositories): the label is issued first
// (the journey's foreign key), then the journey is driven to IN_TRANSIT by
// two applied scans.
func seedJourneyInPostgres(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	labels, journeys := postgres.NewLabelRepo(pool), postgres.NewJourneyRepo(pool)

	t0 := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	l, _, err := label.Issue(label.NewLabel{
		Ref: "lbl-1", TrackingNumber: "RN0123456789ABCDEF", PONumber: "PO-1", PackageNo: 1,
		AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
		StopKey: strings.Repeat("ab", label.StopKeyLen/2), DestinationPostalPrefix: "01310",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := labels.Save(ctx, l); err != nil {
		t.Fatal(err)
	}
	j, err := journey.Start(journey.NewJourney{
		TrackingNumber: shared.TrackingNumber(l.TrackingNumber()), LabelRef: string(l.Ref()),
		PONumber: l.PONumber(), AssignmentID: l.AssignmentID(), OrderID: l.OrderID(), Tier: l.Tier(),
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := journeys.Save(ctx, j); err != nil {
		t.Fatal(err)
	}
	for _, scan := range []journey.ScanInput{
		{ID: "scan-it-1", NodeID: "node-A", Type: journey.ScanPickupDeparture, At: t0.Add(time.Hour)},
		{ID: "scan-it-2", NodeID: "node-B", Type: journey.ScanDeparture, At: t0.Add(2 * time.Hour)},
	} {
		if _, err := j.RecordScan(scan, scan.At); err != nil {
			t.Fatal(err)
		}
		if err := journeys.Save(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
}
