package kafka

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"
)

// runDLQ records the dead letters a Run test publishes.
type runDLQ struct {
	msgs []kafkago.Message
}

func (d *runDLQ) WriteMessages(_ context.Context, m ...kafkago.Message) error {
	d.msgs = append(d.msgs, m...)
	return nil
}

func (d *runDLQ) Close() error { return nil }

// cloudEvent builds a minimal CloudEvents 1.0 message of the given type, the
// wire form a consumer's HandleMessage dispatches on.
func cloudEvent(id, typ string) []byte {
	return []byte(`{"specversion":"1.0","id":"` + id + `","source":"/t","type":"` + typ +
		`","datacontenttype":"application/json","data":{}}`)
}

func runConsumer(t *testing.T, c *Consumer, msgs ...kafkago.Message) *loopRecorder {
	t.Helper()
	rec := &loopRecorder{}
	c.Reader = &scriptedReader{rec: rec, msgs: msgs}
	if c.Logger == nil {
		c.Logger = quietLogger()
	}
	if c.Retry == (RetryPolicy{}) {
		c.Retry = RetryPolicy{Initial: time.Millisecond, Max: time.Millisecond}
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return rec
}

// Run wires the consumer's Reader, handlers, retry policy and dead-letter
// sink into the shared at-least-once loop: every fetched message is committed
// only after HandleMessage returned nil, and cancelling the context ends Run
// with that error (the composition root's shutdown path).
func TestRunCommitsAfterSuccessAndStopsOnContextCancel(t *testing.T) {
	c := &Consumer{
		Name:     "run consumer",
		Topic:    "t.run",
		Handlers: map[string]HandlerFunc{}, // no handled types: both messages are acknowledged
		DLQ:      &runDLQ{},
	}
	rec := runConsumer(t, c,
		kafkago.Message{Topic: "t.run", Partition: 0, Offset: 1, Value: []byte("not a cloudevent")},
		kafkago.Message{Topic: "t.run", Partition: 0, Offset: 2, Value: cloudEvent("e2", "unhandled.Type")},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, rec, "commit:2", "the loop never committed both acknowledged messages")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

// Run must wire the dead-letter sink and the domain attempt bound: a handled
// message that keeps failing transiently is dead-lettered to <topic>.dlq after
// domainMaxHandlerAttempts attempts and the loop commits past it.
func TestRunDeadLettersAMessageThatKeepsFailing(t *testing.T) {
	c := &Consumer{
		Name:  "dlq consumer",
		Topic: "t.dlq",
		Handlers: map[string]HandlerFunc{
			"test.Fail": func(context.Context, ce.Event) error { return errors.New("transient") },
		},
		DLQ: &runDLQ{},
	}
	dlq := c.DLQ.(*runDLQ)
	rec := runConsumer(t, c,
		kafkago.Message{Topic: "t.dlq", Partition: 3, Offset: 7, Key: []byte("k"), Value: cloudEvent("e7", "test.Fail")},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, rec, "commit:7", "the loop never committed past the dead-lettered message")
	cancel()
	<-done

	if len(dlq.msgs) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dlq.msgs))
	}
	m := dlq.msgs[0]
	if m.Topic != "t.dlq.dlq" || string(m.Key) != "k" {
		t.Errorf("dead letter = %+v, want topic t.dlq.dlq with the original key", m)
	}
	if string(m.Value) != string(cloudEvent("e7", "test.Fail")) {
		t.Errorf("dead letter value = %s, want the original message bytes", m.Value)
	}
	var sourceTopic, sourceOffset bool
	for _, h := range m.Headers {
		if h.Key == "x-dlq-source-topic" && string(h.Value) == "t.dlq" {
			sourceTopic = true
		}
		if h.Key == "x-dlq-source-offset" && string(h.Value) == "7" {
			sourceOffset = true
		}
	}
	if !sourceTopic || !sourceOffset {
		t.Errorf("dead letter headers = %v, want x-dlq-source-topic and x-dlq-source-offset", m.Headers)
	}
}

// A consumer without a DLQ keeps the unbounded-retry behaviour: a transiently
// failing message is retried forever and never committed.
func TestRunWithoutDLQRetriesTheSameMessageForever(t *testing.T) {
	var attempts atomic.Int32
	c := &Consumer{
		Name: "no-dlq consumer", Topic: "t.plain",
		Handlers: map[string]HandlerFunc{
			"test.Fail": func(context.Context, ce.Event) error { attempts.Add(1); return errors.New("transient") },
		},
	}
	rec := runConsumer(t, c,
		kafkago.Message{Topic: "t.plain", Partition: 0, Offset: 1, Value: cloudEvent("e1", "test.Fail")},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(5 * time.Second)
	for attempts.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("the loop stopped retrying after %d attempts; events: %v", attempts.Load(), rec.log())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if strings.Contains(strings.Join(rec.log(), " "), "commit:1") {
		t.Error("a message that always failed must never be committed")
	}
}

// waitFor blocks until the recorder's log contains want.
func waitFor(t *testing.T, rec *loopRecorder, want, msg string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(strings.Join(rec.log(), " "), want) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s; events: %v", msg, rec.log())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
