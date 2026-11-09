//go:build integration

package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	relay "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/application/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
	"github.com/claudioed/shipping-and-tracking/internal/testing/kafkatest"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// The whole analytics read side (ADR 0012) against real infrastructure, all
// started with testcontainers (never skip-gated, never a DSN from env): the REAL
// use cases write labels, journeys and outbox rows to the OLTP Postgres, the
// REAL encoder shapes the CloudEvents, the relay drains them to a real Kafka
// (losing one ack, so a row is republished under the SAME id), the projector's
// consumer projects this context's own events topic into a SEPARATE analytical
// Postgres (its own container), and the reports answer from it over a read-only
// pool -- while legacy, unknown-type, poison and duplicate messages share the
// topic. The DLQ is read back, and a later marker event proves the duplicate
// was consumed, not stuck.

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func e2eAnalyticalPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("shipping_analytics"), tcpostgres.WithUsername("itest"), tcpostgres.WithPassword("itest"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatalf("start the analytical postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// lostAckOnce delivers to the real sink and THEN reports failure once: the
// "published, but the row was not marked" crash window, so the relay
// republishes the first row with the SAME CloudEvents id.
type lostAckOnce struct {
	inner relay.Sink
	mu    sync.Mutex
	fired bool
}

func (f *lostAckOnce) Send(ctx context.Context, msgs ...outbox.Message) error {
	if err := f.inner.Send(ctx, msgs...); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fired {
		f.fired = true
		return errors.New("simulated crash after the broker ack, before the row was marked published")
	}
	return nil
}

func e2eAt(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }

// e2eWrites drives the real use cases over the OLTP database with a clock the
// test moves to just after each fact.
type e2eWrites struct {
	t     *testing.T
	deps  usecases.Deps
	clock *memory.Clock
	order int
}

func (e *e2eWrites) label(tier shared.Tier) shared.TrackingNumber {
	e.t.Helper()
	e.order++
	e.clock.Set(e2eAt(3, 7))
	v, err := usecases.NewIssueLabel(e.deps).Execute(context.Background(), usecases.IssueLabelCmd{
		PONumber: fmt.Sprintf("PO-E2E-%d", e.order), PackageNo: 1, AssignmentID: fmt.Sprintf("asg-%d", e.order), OrderID: "ord-e2e", Tier: tier,
	})
	if err != nil {
		e.t.Fatalf("issue label %d: %v", e.order, err)
	}
	return shared.TrackingNumber(v.TrackingNumber)
}

func (e *e2eWrites) scans(tn shared.TrackingNumber, steps ...any) {
	e.t.Helper()
	for i := 0; i < len(steps); i += 2 {
		typ, at := steps[i].(journey.ScanType), steps[i+1].(time.Time)
		e.clock.Set(at.Add(time.Minute))
		if _, err := usecases.NewRecordScan(e.deps).Execute(context.Background(), usecases.RecordScanCmd{
			TrackingNumber: tn, ScanID: fmt.Sprintf("%s-%d", tn, i), NodeID: "NODE-1", Type: typ, At: at,
		}); err != nil {
			e.t.Fatalf("%s %s: %v", tn, typ, err)
		}
	}
}

func (e *e2eWrites) returnToSender(tn shared.TrackingNumber, at time.Time) {
	e.t.Helper()
	e.clock.Set(at.Add(time.Minute))
	if r, err := usecases.NewReturnToSender(e.deps).Execute(context.Background(), usecases.ReturnCmd{
		Consumer: "e2e", EventID: "rts-" + string(tn), TrackingNumber: tn, ReturnedAt: at,
	}); err != nil || !r.Returned {
		e.t.Fatalf("return %s: %+v %v", tn, r, err)
	}
}

func (e *e2eWrites) sweep(at time.Time) usecases.SweepResult {
	e.t.Helper()
	e.clock.Set(at)
	r, err := usecases.NewSweepSilence(e.deps).Execute(context.Background())
	if err != nil {
		e.t.Fatalf("sweep at %s: %v", at, err)
	}
	return r
}

func TestAnalyticsReadSide_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	brokers := kafkatest.Brokers(t)
	oltp := pgtest.NewPool(t)
	analyticalURL := e2eAnalyticalPostgres(t)
	if err := analyticsstore.RunMigrations(analyticalURL); err != nil {
		t.Fatal(err)
	}
	topic := kafkatest.Topic(t, kafka.OwnEventsTopic)
	dlqTopic := kafkatest.Topic(t, kafka.AnalyticsDLQTopic)

	// ---- OLTP writes through the real use cases and the real encoder ----
	gateway := memory.NewShipToGateway()
	gateway.Put("ord-e2e", shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", Region: "SP", PostalCode: "01310-100", CountryCode: "BR"})
	clock := memory.NewClock(e2eAt(3, 7))
	w := &e2eWrites{t: t, clock: clock, deps: usecases.Deps{
		Labels: postgres.NewLabelRepo(oltp), Snapshots: postgres.NewSnapshotRepo(oltp), Journeys: postgres.NewJourneyRepo(oltp),
		Catalog: postgres.NewCatalogRepo(oltp), Inbox: postgres.NewProcessedEventRepo(oltp),
		Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(oltp), &outboundkafka.Encoder{Topic: topic}),
		UoW:    postgres.NewUnitOfWork(oltp), Clock: clock, IDs: &memory.SequentialIDs{}, ShipTo: gateway,
		Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
	}}
	a, b, c := w.label("NEXT_DAY"), w.label("NEXT_DAY"), w.label("STANDARD")
	d := w.label("SAME_DAY")
	// A: delivered at the first attempt; SHIPMENT_CONFIRMED and DELIVERED are not touches -> 3 touches.
	w.scans(a, journey.ScanShipmentConfirmed, e2eAt(3, 8), journey.ScanPickupDeparture, e2eAt(3, 9), journey.ScanStationArrival, e2eAt(3, 10),
		journey.ScanOutForDelivery, e2eAt(3, 11), journey.ScanDelivered, e2eAt(3, 12))
	// B: one failed attempt, delivered at the second -> 5 touches, 2 attempts.
	w.scans(b, journey.ScanPickupDeparture, e2eAt(3, 9), journey.ScanStationArrival, e2eAt(3, 10), journey.ScanOutForDelivery, e2eAt(3, 11),
		journey.ScanDeliveryFailed, e2eAt(3, 13), journey.ScanOutForDelivery, e2eAt(3, 14), journey.ScanDelivered, e2eAt(3, 15))
	// C: a failed attempt, then returned to the sender.
	w.scans(c, journey.ScanPickupDeparture, e2eAt(3, 9), journey.ScanStationArrival, e2eAt(3, 10), journey.ScanOutForDelivery, e2eAt(3, 11),
		journey.ScanDeliveryFailed, e2eAt(3, 13))
	w.returnToSender(c, e2eAt(3, 16))
	// D (SAME_DAY): in transit, then silent -> an exception, then declared lost two days later.
	w.scans(d, journey.ScanPickupDeparture, e2eAt(3, 9), journey.ScanDeparture, e2eAt(3, 10))
	if r := w.sweep(e2eAt(3, 20)); r.Raised != 1 {
		t.Fatalf("first sweep = %+v, want one exception (D)", r)
	}
	if r := w.sweep(e2eAt(5, 21)); r.Lost != 1 {
		t.Fatalf("second sweep = %+v, want D declared lost", r)
	}

	// ---- the topic also carries messages the projector must survive ----
	legacy := []byte(`{"event_id":"e-1","event_type":"PackageDelivered","payload":{"trackingNumber":"x"}}`)
	unknown := hand(t, "evt-unknown", cloudevents.Type("journey", "PackageTeleported"), "STT-U", e2eAt(3, 13), map[string]any{"trackingNumber": "STT-U"})
	poison := hand(t, "evt-poison", cloudevents.Type("journey", "PackageLost"), "STT-P", e2eAt(3, 13),
		map[string]any{"trackingNumber": "STT-OTHER", "orderId": "o", "declaredAt": e2eAt(3, 13)}) // subject != aggregate id
	kafkatest.Produce(t, topic, kafkatest.Message{Key: "x", Value: legacy}, kafkatest.Message{Key: "STT-U", Value: unknown},
		kafkatest.Message{Key: "STT-P", Value: poison})

	// ---- relay -> real Kafka (with one lost ack: a republish under the SAME id) ----
	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	r := relay.NewRelay(postgres.NewOutboxRepo(oltp), &lostAckOnce{inner: sink}, quiet, relay.WithInterval(50*time.Millisecond))
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); _ = r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-relayDone })

	// ---- the projector: consumer + writer pool over the analytical database ----
	writer, err := analyticsstore.NewPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	consumer := kafka.NewAnalyticsConsumer(brokers, topic, dlqTopic, fmt.Sprintf("st-projector-e2e-%d", time.Now().UnixNano()),
		analyticsstore.NewProjection(writer), quiet)
	consumer.Retry = kafka.RetryPolicy{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond}
	consumerDone := make(chan struct{})
	go func() { defer close(consumerDone); _ = consumer.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-consumerDone; _ = consumer.Close() })

	// ---- the reports over a READ-ONLY pool ----
	readOnly, err := analyticsstore.NewReadOnlyPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	reader := analyticsstore.NewReader(readOnly)
	srv := httptest.NewServer(reportshttp.NewRouter(&reportshttp.Server{Reader: reader, Ping: reader, Now: func() time.Time { return e2eAt(6, 0) }}, quiet, "e2e"))
	t.Cleanup(srv.Close)
	const span = "?from=2026-11-03&to=2026-11-05"
	outcomes := func() []map[string]any {
		var out struct {
			Days []map[string]any `json:"days"`
		}
		getJSON(t, srv.URL+"/reports/delivery-outcomes"+span, &out)
		return out.Days
	}
	waitFor(t, 90*time.Second, "D's PackageLost (the last OLTP event) projected", func() bool {
		var f struct {
			AsOf *time.Time `json:"asOf"`
		}
		getJSON(t, srv.URL+"/reports/freshness", &f)
		return f.AsOf != nil && f.AsOf.Equal(e2eAt(5, 21))
	})

	var touches struct {
		From, To string
		Days     []map[string]any `json:"days"`
	}
	getJSON(t, srv.URL+"/reports/package-touches"+span, &touches)
	wantTouches := []map[string]any{{"day": "2026-11-03", "tier": "NEXT_DAY", "packagesDelivered": 2.0,
		"meanTouchesPerPackage": 4.0, "p50TouchesPerPackage": 4.0, "p90TouchesPerPackage": 4.8}}
	if !reflect.DeepEqual(touches.Days, wantTouches) || touches.From != "2026-11-03" || touches.To != "2026-11-05" {
		t.Errorf("package-touches = %+v\nwant days %v", touches, wantTouches)
	}
	wantOutcomes := []map[string]any{
		{"day": "2026-11-03", "packagesDelivered": 2.0, "firstAttemptDelivered": 1.0, "packagesReturnedToSender": 1.0, "packagesLost": 0.0,
			"firstAttemptSuccessRate": 1.0 / 3, "meanAttemptsPerDeliveredPackage": 1.5, "lostRate": 0.0, "returnToSenderRate": 1.0 / 3},
		{"day": "2026-11-05", "packagesDelivered": 0.0, "firstAttemptDelivered": 0.0, "packagesReturnedToSender": 0.0, "packagesLost": 1.0,
			"firstAttemptSuccessRate": 0.0, "meanAttemptsPerDeliveredPackage": nil, "lostRate": 1.0, "returnToSenderRate": 0.0},
	}
	if got := outcomes(); !reflect.DeepEqual(got, wantOutcomes) {
		t.Errorf("delivery-outcomes = %v\nwant %v", got, wantOutcomes)
	}
	assertOnlyThePoisonWasDeadLettered(t, brokers, dlqTopic, poison)

	// ---- a redelivered PackageDelivered (same id) changes nothing; a later marker proves it was consumed ----
	var dup []byte
	if err := oltp.QueryRow(ctx, `SELECT value FROM outbox_events WHERE topic = $1 AND subject = $2 AND event_type LIKE '%PackageDelivered' LIMIT 1`,
		topic, string(a)).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	marker := hand(t, "evt-marker", cloudevents.Type("journey", "PackageLost"), "STT-MARKER", e2eAt(5, 22),
		map[string]any{"trackingNumber": "STT-MARKER", "orderId": "ord-marker", "declaredAt": e2eAt(5, 22), "version": 3, "at": e2eAt(5, 22)})
	kafkatest.Produce(t, topic, kafkatest.Message{Key: string(a), Value: dup}, kafkatest.Message{Key: "STT-MARKER", Value: marker})
	waitFor(t, 60*time.Second, "the marker projected", func() bool {
		days := outcomes()
		return len(days) == 2 && days[1]["packagesLost"] == 2.0
	})
	if day3 := outcomes()[0]; day3["packagesDelivered"] != 2.0 {
		t.Errorf("after a redelivered PackageDelivered day 3 delivered = %v, want still 2 (idempotent on the CloudEvents id)", day3["packagesDelivered"])
	}
	pgtest.WaitAllPublished(t, oltp)
}

// assertOnlyThePoisonWasDeadLettered reads the DLQ: exactly the poison bytes,
// nothing for the legacy or unknown-type messages.
func assertOnlyThePoisonWasDeadLettered(t *testing.T, brokers []string, dlqTopic string, poison []byte) {
	t.Helper()
	dlq := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: dlqTopic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer func() { _ = dlq.Close() }()
	readCtx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	m, err := dlq.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("nothing reached the DLQ: %v", err)
	}
	if !bytes.Equal(m.Value, poison) {
		t.Errorf("DLQ value differs from the poison message:\n%s", m.Value)
	}
	quietCtx, quietStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer quietStop()
	if extra, err := dlq.ReadMessage(quietCtx); err == nil {
		t.Errorf("a second message reached the DLQ (legacy and unknown types are skipped, not dead-lettered): %s", extra.Value)
	}
}

func hand(t *testing.T, id, typ, subject string, at time.Time, data map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/shipping-and-tracking", "type": typ, "subject": subject,
		"datacontenttype": "application/json", "time": at.Format(time.RFC3339), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
	}
}
