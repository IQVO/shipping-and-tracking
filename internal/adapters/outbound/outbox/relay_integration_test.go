//go:build integration

package outbox_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	relay "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/postgres"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/testing/kafkatest"
	"github.com/claudioed/shipping-and-tracking/internal/testing/pgtest"
)

// The relay is proven end to end against real infrastructure, both started with
// testcontainers (never skip-gated, never a hardcoded broker address): the use
// cases write labels, journeys and outbox rows in real Postgres, the relay
// drains them to a real broker on a unique topic, and the CloudEvents are read
// back.

func TestRelay_RealPostgresAndKafka_PublishesCloudEventsKeyedBySubject(t *testing.T) {
	ctx := context.Background()
	brokers := kafkatest.Brokers(t)
	pool := pgtest.NewPool(t)
	topic := kafkatest.Topic(t, outboundkafka.Topic)

	gateway := memory.NewShipToGateway()
	gateway.Put("ord-1", shipto.Address{Name: "Ana Example", Line1: "10 Sample Street", City: "Sampletown", Region: "SP", PostalCode: "01310-100", CountryCode: "BR"})
	deps := usecases.Deps{
		Labels: postgres.NewLabelRepo(pool), Snapshots: postgres.NewSnapshotRepo(pool), Journeys: postgres.NewJourneyRepo(pool),
		Catalog: postgres.NewCatalogRepo(pool), Inbox: postgres.NewProcessedEventRepo(pool),
		Outbox: postgres.NewOutbox(postgres.NewOutboxRepo(pool), &outboundkafka.Encoder{Topic: topic}),
		UoW:    postgres.NewUnitOfWork(pool), Clock: clock.System{}, IDs: &memory.SequentialIDs{}, ShipTo: gateway,
		Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
	}
	v, err := usecases.NewIssueLabel(deps).Execute(ctx, usecases.IssueLabelCmd{
		PONumber: "PO-1", PackageNo: 1, AssignmentID: "asg-1", OrderID: "ord-1", Tier: "NEXT_DAY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := usecases.NewVoidLabel(deps).Execute(ctx, "lbl-1", "order cancelled"); err != nil {
		t.Fatal(err)
	}

	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	r := relay.NewRelay(postgres.NewOutboxRepo(pool), sink, slog.New(slog.NewTextHandler(io.Discard, nil)), relay.WithInterval(100*time.Millisecond))
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Run(runCtx) }()
	t.Cleanup(func() { stop(); <-done })

	prefix := "com.retailnetwork.logistics.shipping-and-tracking.label."
	got := kafkatest.ReadN(t, topic, 2, 60*time.Second)
	for i, want := range []struct{ typ, subject string }{{prefix + "LabelIssued", "lbl-1"}, {prefix + "LabelVoided", "lbl-1"}} {
		assertCloudEvent(t, got[i], want.typ, want.subject)
	}
	pgtest.WaitAllPublished(t, pool)
	_ = v
}

// assertCloudEvent checks the key, the content-type header and the decoded
// CloudEvents attributes of one consumed message.
func assertCloudEvent(t *testing.T, msg kafkatest.Message, wantType, wantSubject string) {
	t.Helper()
	if msg.Key != wantSubject {
		t.Errorf("key = %q, want %s", msg.Key, wantSubject)
	}
	if len(msg.Headers) != 1 || msg.Headers["content-type"] != cloudevents.MediaType {
		t.Errorf("headers = %+v", msg.Headers)
	}
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Type() != wantType || e.Subject() != wantSubject || e.Source() != "/retailnetwork/shipping-and-tracking" {
		t.Fatalf("event = type %q subject %q source %q, want type %q subject %q", e.Type(), e.Subject(), e.Source(), wantType, wantSubject)
	}
}
