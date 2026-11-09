package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// fakeProjection records the applied events; errs are returned one per Apply
// call first (nil entries succeed).
type fakeProjection struct {
	mu     sync.Mutex
	events []report.Event
	errs   []error
}

func (f *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return false, err
		}
	}
	f.events = append(f.events, e)
	return true, nil
}

var (
	eventTime   = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	businessAt  = time.Date(2026, 11, 3, 11, 30, 0, 0, time.UTC)
	businessRFC = "2026-11-03T11:30:00Z"
)

// ownEvent encodes one of this context's events through the shared envelope
// builder (the one the outbox encoder uses).
func ownEvent(t *testing.T, entity, name, subject string, data map[string]any) []byte {
	t.Helper()
	b, err := cloudevents.New(cloudevents.Spec{ID: "evt-" + name, Entity: entity, EventName: name, Subject: subject,
		Time: eventTime, Stream: cloudevents.StreamEvents, Version: 1, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func analyticsConsumer(p report.Projection, dlq DeadLetterWriter, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = quietLogger()
	}
	return &AnalyticsConsumer{Projection: p, DLQ: dlq, Topic: OwnEventsTopic, DLQTopic: AnalyticsDLQTopic, Logger: logger}
}

func TestAnalyticsConsumerProjectsTheFiveTypes(t *testing.T) {
	base := report.Event{At: eventTime, TrackingNumber: "STT1", OccurredAt: businessAt}
	cases := []struct {
		entity, name, subject string
		data                  map[string]any
		want                  func(e report.Event) report.Event
	}{
		{entityLabel, "LabelIssued", "lbl-1", map[string]any{"labelRef": "lbl-1", "trackingNumber": "STT1", "tier": "NEXT_DAY", "carrier": "x"},
			func(e report.Event) report.Event {
				e.Kind, e.LabelRef, e.Tier, e.OccurredAt = report.KindLabelIssued, "lbl-1", "NEXT_DAY", eventTime
				return e
			}},
		{entityJourney, "PackageScanned", "STT1", map[string]any{"trackingNumber": "STT1", "scanId": "s1", "scanType": "DEPARTURE", "at": businessRFC, "state": "IN_TRANSIT"},
			func(e report.Event) report.Event {
				e.Kind, e.ScanID, e.ScanType = report.KindPackageScanned, "s1", "DEPARTURE"
				return e
			}},
		{entityJourney, "PackageDelivered", "STT1", map[string]any{"trackingNumber": "STT1", "deliveredAt": businessRFC},
			func(e report.Event) report.Event { e.Kind = report.KindPackageDelivered; return e }},
		{entityJourney, "PackageReturned", "STT1", map[string]any{"trackingNumber": "STT1", "returnedAt": businessRFC},
			func(e report.Event) report.Event { e.Kind = report.KindPackageReturned; return e }},
		{entityJourney, "PackageLost", "STT1", map[string]any{"trackingNumber": "STT1", "declaredAt": businessRFC},
			func(e report.Event) report.Event { e.Kind = report.KindPackageLost; return e }},
	}
	for _, c := range cases {
		p, dlq := &fakeProjection{}, &runDLQ{}
		err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(),
			kafkago.Message{Value: ownEvent(t, c.entity, c.name, c.subject, c.data)})
		if err != nil || len(dlq.msgs) != 0 || len(p.events) != 1 {
			t.Fatalf("%s: err=%v dlq=%d applied=%d", c.name, err, len(dlq.msgs), len(p.events))
		}
		want := c.want(base)
		want.EventID = "evt-" + c.name
		if !reflect.DeepEqual(p.events[0], want) {
			t.Errorf("%s projected %+v\nwant %+v", c.name, p.events[0], want)
		}
	}
}

func TestAnalyticsConsumerDeadLettersPoisonOnce(t *testing.T) {
	cases := []struct {
		name string
		msg  func(t *testing.T) []byte
		errs []error
	}{
		{"undecodable timestamp", func(t *testing.T) []byte {
			return ownEvent(t, entityJourney, "PackageDelivered", "STT1", map[string]any{"trackingNumber": "STT1", "deliveredAt": "yesterday"})
		}, nil},
		{"subject is not the aggregate", func(t *testing.T) []byte {
			return ownEvent(t, entityJourney, "PackageLost", "STT9", map[string]any{"trackingNumber": "STT1", "declaredAt": businessRFC})
		}, nil},
		{"label subject is not the labelRef", func(t *testing.T) []byte {
			return ownEvent(t, entityLabel, "LabelIssued", "STT1", map[string]any{"labelRef": "lbl-1", "trackingNumber": "STT1", "tier": "NEXT_DAY"})
		}, nil},
		{"a required field missing", func(t *testing.T) []byte {
			return ownEvent(t, entityJourney, "PackageScanned", "STT1", map[string]any{"trackingNumber": "STT1", "scanId": "s1", "at": businessRFC})
		}, nil},
		{"the store rejects it", func(t *testing.T) []byte {
			return ownEvent(t, entityJourney, "PackageLost", "STT1", map[string]any{"trackingNumber": "STT1", "declaredAt": businessRFC})
		}, []error{fmt.Errorf("%w: check violation", report.ErrRejected)}},
	}
	for _, c := range cases {
		p, dlq := &fakeProjection{errs: c.errs}, &runDLQ{}
		value := c.msg(t)
		msg := kafkago.Message{Topic: OwnEventsTopic, Partition: 2, Offset: 7, Key: []byte("STT1"), Value: value}
		if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: poison must be dead-lettered and committed past, got %v", c.name, err)
		}
		if len(dlq.msgs) != 1 || len(p.events) != 0 {
			t.Fatalf("%s: dlq=%d applied=%d, want exactly one dead letter", c.name, len(dlq.msgs), len(p.events))
		}
		d := dlq.msgs[0]
		if d.Topic != AnalyticsDLQTopic || !bytes.Equal(d.Value, value) || string(d.Key) != "STT1" || dlqHeader(d, "x-dlq-source-topic") != OwnEventsTopic {
			t.Errorf("%s: dead letter = topic %s key %s headers %v", c.name, d.Topic, d.Key, d.Headers)
		}
		if dlqHeader(d, "x-dlq-error") == "" {
			t.Errorf("%s: the dead letter must carry the cause", c.name)
		}
	}
}

func dlqHeader(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestAnalyticsConsumerTransientFailureIsReturnedNotDeadLettered(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("connection refused")}}, &runDLQ{}
	msg := kafkago.Message{Value: ownEvent(t, entityJourney, "PackageLost", "STT1", map[string]any{"trackingNumber": "STT1", "declaredAt": businessRFC})}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err == nil {
		t.Fatal("a transient failure must be returned for the loop to retry")
	}
	if len(dlq.msgs) != 0 {
		t.Fatal("a transient failure must never be dead-lettered")
	}
}

// failingDLQ refuses every write.
type failingDLQ struct{}

func (failingDLQ) WriteMessages(context.Context, ...kafkago.Message) error {
	return errors.New("broker down")
}
func (failingDLQ) Close() error { return nil }

func TestAnalyticsConsumerFailedDeadLetterIsRetried(t *testing.T) {
	msg := kafkago.Message{Value: ownEvent(t, entityJourney, "PackageLost", "STT9", map[string]any{"trackingNumber": "STT1", "declaredAt": businessRFC})}
	err := analyticsConsumer(&fakeProjection{}, failingDLQ{}, nil).HandleMessage(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), AnalyticsDLQTopic) {
		t.Fatalf("a failed dead-letter write must be returned (so the message is retried), got %v", err)
	}
}

func TestAnalyticsConsumerRunRetriesTransientFailuresForever(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("down"), errors.New("down"), errors.New("down"), errors.New("down"),
		errors.New("down"), errors.New("down"), nil}}, &runDLQ{}
	c := analyticsConsumer(p, dlq, nil)
	rec := &loopRecorder{}
	c.Reader = &scriptedReader{rec: rec, msgs: []kafkago.Message{{Offset: 1, Value: ownEvent(t, entityJourney, "PackageLost", "STT1",
		map[string]any{"trackingNumber": "STT1", "declaredAt": businessRFC})}}}
	var sleeps []time.Duration
	c.Retry = RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}
	c.sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, rec, "commit:1", "the message is committed once the projection succeeds")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 0 {
		t.Fatalf("applied=%d dlq=%d: six transient failures must be retried past the domain cap, never dead-lettered", len(p.events), len(dlq.msgs))
	}
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want the capped sequence %v", sleeps, want)
	}
}
