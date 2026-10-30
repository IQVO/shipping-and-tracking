//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

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
// are already migrated, a second run must be a no-op), then answers the
// get_journey skill over the real JSON-RPC wire with exactly what the write
// side persisted, and its reads add no outbox row.
func TestA2AAgainstPostgres_ReadsWhatTheApiWroteAndNeverWrites(t *testing.T) {
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

	srv := httptest.NewServer(newAgent(deps, testAgentURL, "test", quietLogger(), "shipping-and-tracking-a2a"))
	t.Cleanup(srv.Close)
	card := testCard()
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}

	before := countOutbox()

	env := sendSkill(t, client, "RN0123456789ABCDEF")
	j, _ := env["data"].(map[string]any)["journey"].(map[string]any)
	if env["problem"] != nil || j == nil {
		t.Fatalf("envelope = %v", env)
	}
	if j["state"] != "IN_TRANSIT" || j["poNumber"] != "PO-1" || j["handedOverAt"] != "2026-10-24T09:00:00Z" {
		t.Fatalf("journey = %v", j)
	}
	if scans, _ := j["scans"].([]any); len(scans) != 2 {
		t.Fatalf("scans = %v, want 2", j["scans"])
	}

	// An unknown tracking number is an in-band 404 problem, not a transport error.
	env = sendSkill(t, client, "RN0000000000000000")
	p, _ := env["problem"].(map[string]any)
	if p == nil || p["status"] != float64(404) || p["type"] != "not-found" {
		t.Fatalf("unknown tracking number: envelope = %v, want a 404 not-found problem", env)
	}

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
	}
}

// sendSkill drives get_journey through the SDK client and returns the decoded
// envelope of the reply's data part.
func sendSkill(t *testing.T, client *a2aclient.Client, trackingNumber string) map[string]any {
	t.Helper()
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser,
		a2a.NewDataPart(map[string]any{"skill": "get_journey", "arguments": map[string]any{"trackingNumber": trackingNumber}}))})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) == 0 {
		t.Fatalf("reply = %T", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env
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
