package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// scriptableWriter is a DeadLetterWriter whose WriteMessages results are
// scripted per call.
type scriptableWriter struct {
	results []error
	calls   int
	msgs    []kafkago.Message
}

func (w *scriptableWriter) WriteMessages(_ context.Context, m ...kafkago.Message) error {
	w.msgs = append(w.msgs, m...)
	var err error
	if w.calls < len(w.results) {
		err = w.results[w.calls]
	}
	w.calls++
	return err
}

func (w *scriptableWriter) Close() error { return nil }

// writeDLQ must retry ONLY the topic-is-being-created states, give up after
// dlqTopicReadyAttempts, and pass any other error through at once.
func TestWriteDLQRetriesWhileTheTopicHasNoLeader(t *testing.T) {
	ctx := context.Background()
	notReady := fmt.Errorf("write failed: %w", kafkago.UnknownTopicOrPartition)

	w := &scriptableWriter{results: []error{notReady, notReady, nil}}
	if err := writeDLQ(ctx, w, kafkago.Message{}); err != nil {
		t.Fatalf("writeDLQ = %v, want nil after the topic appears", err)
	}
	if w.calls != 3 {
		t.Errorf("write calls = %d, want 3", w.calls)
	}
}

func TestWriteDLQReturnsOtherErrorsImmediately(t *testing.T) {
	ctx := context.Background()
	w := &scriptableWriter{results: []error{errors.New("broker down")}}
	if err := writeDLQ(ctx, w, kafkago.Message{}); err == nil || errors.Is(err, kafkago.UnknownTopicOrPartition) {
		t.Fatalf("writeDLQ = %v, want the unrelated error at once", err)
	}
	if w.calls != 1 {
		t.Errorf("write calls = %d, want 1 (no retry of an unrelated error)", w.calls)
	}
}

// A cancelled context stops the bounded wait and joins both errors.
func TestWriteDLQStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &scriptableWriter{results: []error{fmt.Errorf("write failed: %w", kafkago.UnknownTopicOrPartition)}}
	err := writeDLQ(ctx, w, kafkago.Message{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("writeDLQ = %v, want context.Canceled joined in", err)
	}
}

// isTopicNotReady must accept every kind of "auto-created topic not ready"
// signal (single error, per-message WriteErrors, both mixed) and reject
// anything else.
func TestIsTopicNotReady(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unknown topic", fmt.Errorf("w: %w", kafkago.UnknownTopicOrPartition), true},
		{"no leader", fmt.Errorf("w: %w", kafkago.LeaderNotAvailable), true},
		{"one write error not ready", kafkago.WriteErrors{fmt.Errorf("w: %w", kafkago.LeaderNotAvailable)}, true},
		{"mixed write errors", kafkago.WriteErrors{nil, fmt.Errorf("w: %w", kafkago.UnknownTopicOrPartition)}, true},
		{"empty write errors", kafkago.WriteErrors{}, false},
		{"other write error", kafkago.WriteErrors{errors.New("bad record")}, false},
		{"unrelated error", errors.New("broker down"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTopicNotReady(tc.err); got != tc.want {
				t.Errorf("isTopicNotReady(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// publishDeadLetter must carry the original key and value plus the x-dlq-*
// context, and target <topic>.dlq.
func TestPublishDeadLetterCarriesTheOriginalMessageAndDLQContext(t *testing.T) {
	ctx := context.Background()
	w := &scriptableWriter{}
	cause := errors.New("handler failed 5 times")
	src := kafkago.Message{
		Topic: "retailnetwork.linehaul-transportation.events", Partition: 2, Offset: 41,
		Key:     []byte("RN1"),
		Value:   []byte(`{"specversion":"1.0"}`),
		Headers: []kafkago.Header{{Key: "content-type", Value: []byte("application/cloudevents+json")}},
	}

	if err := publishDeadLetter(ctx, w, "retailnetwork.linehaul-transportation.events.dlq", src, cause); err != nil {
		t.Fatalf("publishDeadLetter = %v", err)
	}
	if len(w.msgs) != 1 {
		t.Fatalf("published %d messages, want 1", len(w.msgs))
	}
	m := w.msgs[0]
	if m.Topic != "retailnetwork.linehaul-transportation.events.dlq" {
		t.Errorf("topic = %q", m.Topic)
	}
	if string(m.Key) != "RN1" || string(m.Value) != string(src.Value) {
		t.Errorf("key/value were not carried: %q %s", m.Key, m.Value)
	}

	want := map[string]string{
		"content-type":       "application/cloudevents+json",
		"x-dlq-source-topic": src.Topic,
		"x-dlq-error":        cause.Error(),
	}
	got := map[string]string{}
	for _, h := range m.Headers {
		got[h.Key] = string(h.Value)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("header %s = %q, want %q", k, got[k], v)
		}
	}
	if v, ok := got["x-dlq-source-partition"]; !ok || v != strconv.Itoa(src.Partition) {
		t.Errorf("x-dlq-source-partition = %q, want %d", v, src.Partition)
	}
	if v, ok := got["x-dlq-source-offset"]; !ok || v != strconv.FormatInt(src.Offset, 10) {
		t.Errorf("x-dlq-source-offset = %q, want %d", v, src.Offset)
	}
	if v, ok := got["x-dlq-failed-at"]; !ok {
		t.Error("x-dlq-failed-at is missing")
	} else if _, err := time.Parse(time.RFC3339, v); err != nil {
		t.Errorf("x-dlq-failed-at = %q, not RFC3339", v)
	}
}
