package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/outbox"
)

func msg(id string) outbox.Message {
	return outbox.Message{EventID: id, Topic: "t", EventType: "Type" + id, Key: []byte(id), Value: []byte("v-" + id)}
}

func insert(t *testing.T, r *memory.OutboxRepo, ids ...string) {
	t.Helper()
	msgs := make([]outbox.Message, 0, len(ids))
	for _, id := range ids {
		msgs = append(msgs, msg(id))
	}
	if err := r.Insert(context.Background(), msgs...); err != nil {
		t.Fatalf("Insert: %v", err)
	}
}

// Insert/Messages/Unpublished mirror the Postgres outbox_events table: rows
// land in insertion order and start unpublished.
func TestOutboxRepoInsertMessagesUnpublished(t *testing.T) {
	r := memory.NewOutboxRepo()
	insert(t, r, "a", "b", "c")

	got := r.Messages()
	if len(got) != 3 || got[0].EventID != "a" || got[2].EventID != "c" {
		t.Errorf("Messages = %v, want a,b,c in order", got)
	}
	if n := r.Unpublished(); n != 3 {
		t.Errorf("Unpublished = %d, want 3", n)
	}
	if le := r.LastError(1); le != "" {
		t.Errorf("LastError of a fresh row = %q, want empty", le)
	}
}

// Drain publishes oldest first, one at a time, and stops at the first send
// error, reporting it as an *outbox.SendFailure with the 1-based row id and
// the row's attempt count after the failure.
func TestOutboxRepoDrainPublishesOldestFirstAndStopsAtTheFirstError(t *testing.T) {
	r := memory.NewOutboxRepo()
	insert(t, r, "a", "b", "c")

	var sent []string
	cause := errors.New("kafka unreachable")
	published, err := r.Drain(context.Background(), 10, 0, func(_ context.Context, m outbox.Message) error {
		if m.EventID == "b" {
			return cause
		}
		sent = append(sent, m.EventID)
		return nil
	})
	if published != 1 {
		t.Errorf("published = %d, want 1 (only the row before the failure)", published)
	}
	var sf *outbox.SendFailure
	if !errors.As(err, &sf) {
		t.Fatalf("Drain error = %v, want *outbox.SendFailure", err)
	}
	if sf.RowID != 2 || sf.Message.EventID != "b" || sf.Attempts != 1 || !errors.Is(sf.Err, cause) {
		t.Errorf("SendFailure = %+v, want row 2 of event b after 1 attempt wrapping the cause", sf)
	}
	if err.Error() != "outbox row 2 (Typeb): kafka unreachable" {
		t.Errorf("Error() = %q, want the runbook wording", err.Error())
	}
	if len(sent) != 1 || sent[0] != "a" {
		t.Errorf("sent = %v, want only a (c must not be tried after the failure)", sent)
	}
	if n := r.Unpublished(); n != 2 {
		t.Errorf("Unpublished = %d, want 2 (b and c)", n)
	}
	if le := r.LastError(1); le != "kafka unreachable" {
		t.Errorf("LastError(1) = %q, want the recorded cause", le)
	}
}

// A row that reached the attempt cap is skipped by later passes (ADR 0008)
// instead of blocking the drain forever.
func TestOutboxRepoDrainSkipsRowsPastTheAttemptCap(t *testing.T) {
	r := memory.NewOutboxRepo()
	insert(t, r, "poison", "healthy")

	attempts := 0
	failing := func(_ context.Context, m outbox.Message) error {
		if m.EventID == "poison" {
			attempts++
			return errors.New("still failing")
		}
		return nil
	}
	if _, err := r.Drain(context.Background(), 10, 3, failing); err == nil {
		t.Fatal("first pass should fail on the poison row")
	}
	if attempts != 1 {
		t.Fatalf("poison attempts = %d, want 1", attempts)
	}

	// Second and third passes: the poison row is retried up to its cap.
	if _, err := r.Drain(context.Background(), 10, 3, failing); err == nil {
		t.Fatal("second pass should still fail on the poison row")
	}
	if _, err := r.Drain(context.Background(), 10, 3, failing); err == nil {
		t.Fatal("third pass should still fail on the poison row")
	}
	if attempts != 3 {
		t.Fatalf("poison attempts = %d, want 3 (the cap)", attempts)
	}

	// Fourth pass: the poison row has no attempts left and is skipped, so
	// the healthy row behind it finally goes out.
	published, err := r.Drain(context.Background(), 10, 3, failing)
	if err != nil || published != 1 {
		t.Fatalf("fourth pass = (%d, %v), want the healthy row published", published, err)
	}
	if n := r.Unpublished(); n != 1 {
		t.Errorf("Unpublished = %d, want 1 (only the capped poison row)", n)
	}
	if le := r.LastError(0); le == "" {
		t.Error("the poison row must keep its last error recorded")
	}
}

// Drain respects the limit and takes rows in insertion order.
func TestOutboxRepoDrainRespectsTheLimit(t *testing.T) {
	r := memory.NewOutboxRepo()
	insert(t, r, "a", "b", "c")

	published, err := r.Drain(context.Background(), 2, 0, func(context.Context, outbox.Message) error { return nil })
	if err != nil || published != 2 {
		t.Fatalf("Drain = (%d, %v), want 2 published", published, err)
	}
	if n := r.Unpublished(); n != 1 {
		t.Errorf("Unpublished = %d, want 1", n)
	}
}

func TestOutboxRepoDrainOfAnEmptyOutbox(t *testing.T) {
	r := memory.NewOutboxRepo()
	published, err := r.Drain(context.Background(), 10, 0, func(context.Context, outbox.Message) error {
		t.Error("send must not be called on an empty outbox")
		return nil
	})
	if err != nil || published != 0 {
		t.Fatalf("Drain = (%d, %v), want (0, nil)", published, err)
	}
}
