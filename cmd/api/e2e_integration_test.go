//go:build integration

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka and a stand-in customer-ordering, with nothing but the environment a
// deployment sets, and drives it only through REST and Kafka. It reads the
// effects from Postgres and from the output topic, restarts the service, and
// finally takes Kafka away to watch /readyz follow it. Every Kafka read is
// bounded by a deadline; run it with -timeout 600s.

const (
	topicVendor   = "retailnetwork.vendor-connectivity.events"
	topicLinehaul = "retailnetwork.linehaul-transportation.events"
	topicLastMile = "retailnetwork.last-mile-delivery.events"
	topicCatalog  = "retailnetwork.offer-availability.events"
	topicOut      = "retailnetwork.shipping-and-tracking.events"

	outSource  = "/retailnetwork/shipping-and-tracking"
	outPrefix  = "com.retailnetwork.logistics.shipping-and-tracking."
	typeLoadUp = "com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted"
	typeLoadIn = "com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived"
	typeOutFor = "com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery"
	typeTried  = "com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted"
	typeItem   = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated"

	containerStartTimeout = 120 * time.Second
)

var testKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("shipping_e2e"), tcpostgres.WithUsername("e2e"), tcpostgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return url
}

func startKafka(t *testing.T) (*tckafka.KafkaContainer, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("shipping-and-tracking-e2e"))
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return c, brokers
}

// createTopic creates a one-partition topic and waits for its leader.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
	}
	t.Fatalf("topic %s never got a leader", topic)
}

func produce(t *testing.T, brokers []string, topic string, msgs ...kafkago.Message) {
	t.Helper()
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond}
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, msgs...); err != nil {
		t.Fatalf("produce to %s: %v", topic, err)
	}
}

// inputEvent builds a CloudEvent as a sibling context publishes it.
func inputEvent(t *testing.T, id, typ, data string) kafkago.Message {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/retailnetwork/sibling")
	e.SetType(typ)
	e.SetTime(time.Now().UTC())
	if err := e.SetData("application/json", json.RawMessage(data)); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return kafkago.Message{Key: []byte(id), Value: b}
}

// readAll reads every message of the one-partition topic from the start until
// the topic stays quiet for the settle period, bounded by a deadline.
func readAll(t *testing.T, brokers []string, topic string, settle time.Duration) []kafkago.Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer func() { _ = r.Close() }()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	var out []kafkago.Message
	for {
		ctx, cancel := context.WithTimeout(context.Background(), settle)
		m, err := r.ReadMessage(ctx)
		cancel()
		if err != nil {
			return out
		}
		out = append(out, m)
	}
}

func outputTypes(t *testing.T, brokers []string, settle time.Duration) []string {
	t.Helper()
	var types []string
	for _, m := range readAll(t, brokers, topicOut, settle) {
		e := ce.New()
		if err := json.Unmarshal(m.Value, &e); err != nil {
			t.Fatalf("output message is not a CloudEvent: %v", err)
		}
		if e.Source() != outSource || !strings.HasPrefix(e.Type(), outPrefix) {
			t.Fatalf("unexpected output event source %q type %q", e.Source(), e.Type())
		}
		for _, leak := range []string{"Sample Street", "Ana Example", "Sampletown", "01310-100", "5550"} {
			if strings.Contains(string(m.Value), leak) {
				t.Fatalf("%q leaked into a published event: %s", leak, m.Value)
			}
		}
		types = append(types, strings.TrimPrefix(e.Type(), outPrefix))
	}
	return types
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// customerOrdering is the stand-in sibling: ord-1 has a synthetic ship-to.
func customerOrdering(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders/ord-1/ship-to" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"Ana Example","line1":"10 Sample Street","city":"Sampletown","region":"SP","postalCode":"01310-100","countryCode":"BR","phone":"+55 11 5550 0100"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// service is one running instance of the real entry point.
type service struct {
	t      *testing.T
	base   string
	cancel context.CancelFunc
	done   chan error
}

type stack struct {
	dbURL   string
	brokers []string
	orders  string
	addr    string
}

func (s stack) apply(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"DATABASE_URL": s.dbURL, "MIGRATIONS_DATABASE_URL": "", "KAFKA_BROKERS": strings.Join(s.brokers, ","), "EVENT_PUBLISHER": "kafka",
		"CUSTOMER_ORDERING_URL": s.orders, "SHIP_TO_ENCRYPTION_KEY": testKey, "STOP_KEY_SECRET": "e2e-stop-key-secret",
		"VENDOR_CONSUMER_GROUP": "sat-vendor-e2e", "LINEHAUL_CONSUMER_GROUP": "sat-linehaul-e2e",
		"LASTMILE_CONSUMER_GROUP": "sat-lastmile-e2e", "CATALOG_CONSUMER_GROUP": "sat-catalog-e2e",
		"HTTP_ADDR": s.addr, "SHUTDOWN_DRAIN_DELAY": "0", "OUTBOX_RELAY_INTERVAL": "200ms", "SILENCE_SWEEP_INTERVAL": "1h",
		"HOUSEKEEPING_INTERVAL": "1h", "LOG_LEVEL": "warn",
	} {
		t.Setenv(k, v)
	}
}

func (s stack) start(t *testing.T) *service {
	t.Helper()
	s.apply(t)
	ctx, cancel := context.WithCancel(context.Background())
	svc := &service{t: t, base: "http://" + s.addr, cancel: cancel, done: make(chan error, 1)}
	go func() { svc.done <- run(ctx, os.Getenv) }()
	t.Cleanup(svc.stopQuietly)
	waitFor(t, 90*time.Second, "the service to listen on "+s.addr, func() bool {
		select {
		case err := <-svc.done:
			t.Fatalf("run returned while booting: %v", err)
		default:
		}
		status, _ := svc.try(http.MethodGet, "/healthz", "", "")
		return status == http.StatusOK
	})
	return svc
}

func (s *service) stop() {
	s.t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(60 * time.Second):
		s.t.Fatal("run did not return after the shutdown")
	}
	s.done <- nil
}

func (s *service) stopQuietly() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
	}
}

func (s *service) try(method, path, body, key string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		return -1, err.Error()
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return -1, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (s *service) call(method, path, body, key string, want int) string {
	s.t.Helper()
	status, raw := s.try(method, path, body, key)
	if status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
	}
	return raw
}

func waitFor(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func field(t *testing.T, raw, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	s, _ := m[name].(string)
	return s
}

func journeyState(s *service, tn string) string {
	status, raw := s.try(http.MethodGet, "/journeys/"+tn, "", "")
	if status != http.StatusOK {
		return ""
	}
	var j struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal([]byte(raw), &j)
	return j.State
}

func scalar(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEndToEnd_RealPostgresAndKafka(t *testing.T) {
	dbURL := startPostgres(t)
	kafkaC, brokers := startKafka(t)
	for _, topic := range []string{topicVendor, topicLinehaul, topicLastMile, topicCatalog, topicOut} {
		createTopic(t, brokers, topic)
	}
	orders := customerOrdering(t)
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := stack{dbURL: dbURL, brokers: brokers, orders: orders.URL, addr: freeAddr(t)}
	svc := st.start(t)

	// 1. IssueLabel over REST: 201, replayed by its Idempotency-Key.
	body := `{"poNumber":"PO-1","packageNo":1,"assignmentId":"asg-1","orderId":"ord-1","tier":"NEXT_DAY"}`
	first := svc.call(http.MethodPost, "/labels", body, "k1", http.StatusCreated)
	if again := svc.call(http.MethodPost, "/labels", body, "k1", http.StatusCreated); again != first {
		t.Fatalf("replay differs:\n%s\n%s", first, again)
	}
	tn := field(t, first, "trackingNumber")
	if tn == "" || field(t, first, "carrier") != "RN-EXPRESS" || strings.Contains(first, "Sample") {
		t.Fatalf("label = %s", first)
	}
	svc.call(http.MethodPost, "/labels", body, "k2", http.StatusConflict) // label-already-issued
	if scalar(t, pool, `SELECT count(*) FROM shipping_labels`) != 1 || scalar(t, pool, `SELECT count(*) FROM ship_to_snapshots`) != 1 {
		t.Fatal("exactly one label and one snapshot must be stored")
	}
	if scalar(t, pool, `SELECT count(*) FROM ship_to_snapshots WHERE position('Sample'::bytea in ciphertext) > 0`) != 0 {
		t.Fatal("the snapshot must be encrypted")
	}

	// 2. The sibling contexts' events drive the journey to DELIVERED.
	at := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	produce(t, brokers, topicLinehaul, inputEvent(t, "ev-up", typeLoadUp,
		fmt.Sprintf(`{"originNodeId":"fc-1","trackingNumbers":[%q,"RN-UNKNOWN"],"departedAt":%q,"departureKind":"PICKUP"}`, tn, at)))
	waitFor(t, 60*time.Second, "HANDED_OVER", func() bool { return journeyState(svc, tn) == "HANDED_OVER" })
	produce(t, brokers, topicLinehaul,
		inputEvent(t, "ev-up", typeLoadUp, fmt.Sprintf(`{"originNodeId":"fc-1","trackingNumbers":[%q],"departedAt":%q,"departureKind":"PICKUP"}`, tn, at)), // redelivery
		inputEvent(t, "ev-in", typeLoadIn, fmt.Sprintf(`{"destinationNodeId":"ds-1","trackingNumbers":[%q],"arrivedAt":%q,"nodeType":"DELIVERY_STATION"}`, tn, at)))
	waitFor(t, 60*time.Second, "AT_STATION", func() bool { return journeyState(svc, tn) == "AT_STATION" })
	produce(t, brokers, topicLastMile,
		inputEvent(t, "ev-out", typeOutFor, fmt.Sprintf(`{"trackingNumber":%q,"stationNodeId":"ds-1","at":%q}`, tn, at)),
		inputEvent(t, "ev-done", typeTried, fmt.Sprintf(`{"trackingNumber":%q,"outcome":"DELIVERED","at":%q}`, tn, at)))
	waitFor(t, 60*time.Second, "DELIVERED", func() bool { return journeyState(svc, tn) == "DELIVERED" })
	svc.call(http.MethodPost, "/journeys/"+tn+"/scans",
		fmt.Sprintf(`{"scanId":"late","nodeId":"n","scanType":"DEPARTURE","at":%q}`, time.Now().UTC().Format(time.RFC3339)), "k3", http.StatusConflict) // journey-closed

	// 3. A catalog item lands in the local read model; a poison message is dead-lettered.
	produce(t, brokers, topicCatalog, inputEvent(t, "ev-item", typeItem,
		`{"catalogItemId":"item-1","title":"Lamp","lengthMm":300,"widthMm":200,"heightMm":100,"weightG":900,"hazmat":false,"fragile":true}`))
	waitFor(t, 60*time.Second, "the catalog row", func() bool { return scalar(t, pool, `SELECT count(*) FROM catalog_dimensions`) == 1 })
	produce(t, brokers, topicLinehaul, inputEvent(t, "ev-bad", typeLoadIn, `{"trackingNumbers":"not-a-list"}`))
	dlq := topicLinehaul + ".dlq"
	waitFor(t, 60*time.Second, "the dead letter", func() bool { return len(readAll(t, brokers, dlq, time.Second)) == 1 })

	// 4. The output topic holds the journey's events exactly once, none with an address.
	waitFor(t, 60*time.Second, "the outbox to drain", func() bool {
		return scalar(t, pool, `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})
	types := outputTypes(t, brokers, 2*time.Second)
	count := map[string]int{}
	for _, ty := range types {
		count[ty]++
	}
	for _, want := range []string{"label.LabelIssued", "journey.PackageHandedOver", "journey.PackageDelivered"} {
		if count[want] != 1 {
			t.Errorf("%s published %d times in %v, want exactly once", want, count[want], types)
		}
	}
	if rows := scalar(t, pool, `SELECT count(*) FROM outbox_events`); rows != len(types) {
		t.Errorf("%d outbox rows, %d messages on the topic", rows, len(types))
	}

	// 5. Restart: the migrations are a no-op and the state is intact.
	svc.stop()
	svc = st.start(t)
	if got := journeyState(svc, tn); got != "DELIVERED" {
		t.Fatalf("after the restart the journey is %q", got)
	}

	// 6. Take Kafka away: /readyz follows it, /healthz stays 200.
	if err := kafkaC.Stop(context.Background(), nil); err != nil {
		t.Fatalf("stop kafka: %v", err)
	}
	waitFor(t, 60*time.Second, "/readyz to go 503", func() bool {
		status, raw := svc.try(http.MethodGet, "/readyz", "", "")
		return status == http.StatusServiceUnavailable && strings.Contains(raw, "dependency-unavailable") && strings.Contains(raw, "kafka")
	})
	if status, _ := svc.try(http.MethodGet, "/healthz", "", ""); status != http.StatusOK {
		t.Fatalf("/healthz = %d while Kafka is down, want 200", status)
	}
}
