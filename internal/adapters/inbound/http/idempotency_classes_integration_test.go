//go:build integration

package http_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// The replay class of every problem type decides what the Idempotency-Key
// middleware does with a refusal (ADR 0020): FINAL and OUTCOME are stored and
// replayed, RETRYABLE is not stored and its key is released. These tests drive
// the real router over a real Postgres, one refusal per class.

func scanRows(t *testing.T, pool *pgtest.Pool, scanID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM journey_scans WHERE scan_id = $1`, scanID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func goodAddress() shipto.Address {
	return shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", Region: "SP", PostalCode: "01310-100", CountryCode: "BR"}
}

func issueFor(order, po string) string {
	return strings.Replace(strings.Replace(issueBody, "ord-1", order, 1), "PO-1", po, 1)
}

// expectReplay posts the same request again under the same key and wants the
// stored answer back, byte for byte, with the replay header.
func expectReplay(t *testing.T, post func() (int, http.Header, string), wantCode int, wantBody string) {
	t.Helper()
	code, h, body := post()
	if code != wantCode || body != wantBody || h.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay = %d replayed=%q %s, want the stored %d answer", code, h.Get("Idempotency-Replayed"), body, wantCode)
	}
}

// OUTCOME: the scan is stored WITHOUT moving the state, the unit commits, and
// then the route answers 409. The refusal is stored with the scan row: a retry
// replays it and never meets the duplicate-scan 409 a second execution would.
func TestAScanRefusedAfterItWasStoredIsAnOutcomeCommittedAndReplayed(t *testing.T) {
	srv, pool, _ := pgServer(t)
	_, b := post(t, srv, "/labels", "k-issue", issueBody)
	path := "/journeys/" + between(b, `"trackingNumber":"`, `"`) + "/scans"
	if code, body := post(t, srv, path, "k-s1", scanBody("s1", "PICKUP_DEPARTURE", 1)); code != 200 {
		t.Fatalf("scan 1 = %d %s", code, body)
	}
	for _, tc := range []struct {
		scanID, typ, slug string
		hours             int
	}{
		{"s0", "DEPARTURE", "scan-out-of-order", 0},
		{"s2", "DELIVERED", "illegal-transition", 5},
	} {
		key, body := "k-"+tc.scanID, scanBody(tc.scanID, tc.typ, tc.hours)
		code, _, first := postH(t, srv, path, key, body)
		if code != 409 || !strings.Contains(first, tc.slug) {
			t.Fatalf("%s: first = %d %s, want 409 %s", tc.slug, code, first, tc.slug)
		}
		if n := scanRows(t, pool, tc.scanID); n != 1 {
			t.Fatalf("%s: %d scan rows after the refusal, want 1 (stored, not applied)", tc.slug, n)
		}
		if n := keyRows(t, pool, key); n != 1 {
			t.Fatalf("%s: %d key rows, want the stored answer", tc.slug, n)
		}
		for i := 0; i < 2; i++ {
			expectReplay(t, func() (int, http.Header, string) { return postH(t, srv, path, key, body) }, 409, first)
		}
		if n := scanRows(t, pool, tc.scanID); n != 1 {
			t.Fatalf("%s: %d scan rows after the replays, want still 1", tc.slug, n)
		}
	}
}

// OUTCOME, the webhook twin: a carrier report whose scan has no edge is stored
// unapplied and answered 409, and the stored answer replays.
func TestACarrierEventRefusedAfterItWasStoredIsAnOutcomeToo(t *testing.T) {
	srv, pool, _ := pgServer(t)
	_, b := post(t, srv, "/labels", "k-issue", issueBody)
	at := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	body := `{"carrierId":"c1","carrierEventId":"e1","trackingNumber":"` + between(b, `"trackingNumber":"`, `"`) +
		`","status":"DELIVERED","at":"` + at + `"}`
	code, _, first := postH(t, srv, "/carrier-events", "k-ce", body)
	if code != 409 || !strings.Contains(first, "illegal-transition") {
		t.Fatalf("first = %d %s, want 409 illegal-transition", code, first)
	}
	if n := scanRows(t, pool, "c1:e1"); n != 1 {
		t.Fatalf("%d scan rows after the refusal, want 1 (stored, not applied)", n)
	}
	expectReplay(t, func() (int, http.Header, string) { return postH(t, srv, "/carrier-events", "k-ce", body) }, 409, first)
	if n := scanRows(t, pool, "c1:e1"); n != 1 {
		t.Fatalf("%d scan rows after the replay, want still 1", n)
	}
}

// RETRYABLE: nothing committed and the answer follows data that changes
// (customer-ordering's order and address). No key row is left, the same request
// under the same key executes again and a different body is a new request.
func TestARetryableRefusalIsNotStoredSoItsKeyRetriesTheWork(t *testing.T) {
	srv, pool, gw := pgServer(t)
	suspect := goodAddress()
	suspect.Name, suspect.Line1 = "Test", "Sample Street" // no house number

	if code, body := post(t, srv, "/labels", "k-order", issueFor("ord-9", "PO-9")); code != 422 || !strings.Contains(body, "validation-failed") {
		t.Fatalf("unknown order = %d %s", code, body)
	}
	gw.Put("ord-9", goodAddress())
	if n := keyRows(t, pool, "k-order"); n != 0 {
		t.Fatalf("%d key rows after validation-failed, want 0 (released)", n)
	}
	if code, h, body := postH(t, srv, "/labels", "k-order", issueFor("ord-9", "PO-9")); code != 201 || h.Get("Idempotency-Replayed") != "" {
		t.Fatalf("retry once the order is known = %d replayed=%q %s, want a fresh 201", code, h.Get("Idempotency-Replayed"), body)
	}

	gw.Put("ord-8", suspect)
	if code, body := post(t, srv, "/labels", "k-addr", issueFor("ord-8", "PO-8")); code != 422 || !strings.Contains(body, "address-suspect") {
		t.Fatalf("suspect address = %d %s", code, body)
	}
	if n := keyRows(t, pool, "k-addr"); n != 0 {
		t.Fatalf("%d key rows after address-suspect, want 0 (released)", n)
	}
	gw.Put("ord-8", goodAddress())
	if code, body := post(t, srv, "/labels", "k-addr", issueFor("ord-8", "PO-8")); code != 201 {
		t.Fatalf("retry once the address is corrected = %d %s, want 201", code, body)
	}

	if code, _ := post(t, srv, "/labels", "k-free", issueFor("ord-7", "PO-7")); code != 422 {
		t.Fatalf("first body = %d, want 422", code)
	}
	gw.Put("ord-7", goodAddress())
	if code, body := post(t, srv, "/labels", "k-free", issueFor("ord-7", "PO-6")); code != 201 || strings.Contains(body, "idempotency-key-reused") {
		t.Fatalf("another body under the released key = %d %s, want a new request: 201", code, body)
	}
}

// FINAL: the answer follows a fact that never reverts (an id this service
// minted and never deletes, a label that covers its package for good, a scan
// applied once). Stored and replayed.
func TestAFinalRefusalIsStoredAndReplayed(t *testing.T) {
	srv, pool, _ := pgServer(t)
	_, b := post(t, srv, "/labels", "k-issue", issueBody)
	scans := "/journeys/" + between(b, `"trackingNumber":"`, `"`) + "/scans"
	if code, body := post(t, srv, scans, "k-s1", scanBody("s1", "PICKUP_DEPARTURE", 1)); code != 200 {
		t.Fatalf("scan 1 = %d %s", code, body)
	}
	for _, tc := range []struct {
		name, path, key, body, slug string
		status                      int
	}{
		{"not found", "/labels/nope/voids", "f-404", `{"reason":"x"}`, "not-found", 404},
		{"label already issued", "/labels", "f-dup", issueBody, "label-already-issued", 409},
		{"duplicate scan", scans, "f-scan", scanBody("s1", "PICKUP_DEPARTURE", 1), "duplicate-scan", 409},
	} {
		code, _, first := postH(t, srv, tc.path, tc.key, tc.body)
		if code != tc.status || !strings.Contains(first, tc.slug) {
			t.Fatalf("%s: first = %d %s, want %d %s", tc.name, code, first, tc.status, tc.slug)
		}
		if n := keyRows(t, pool, tc.key); n != 1 {
			t.Fatalf("%s: %d key rows, want the stored answer", tc.name, n)
		}
		expectReplay(t, func() (int, http.Header, string) { return postH(t, srv, tc.path, tc.key, tc.body) }, tc.status, first)
	}
}
