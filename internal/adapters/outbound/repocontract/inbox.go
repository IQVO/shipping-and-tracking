package repocontract

import (
	"context"
	"errors"
	"testing"
)

var inboxCases = []named{
	{"a claim is true once per consumer and event id", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		for _, c := range []struct {
			consumer, id string
			want         bool
		}{
			{"c1", "e1", true}, {"c1", "e1", false}, {"c2", "e1", true}, {"c1", "e2", true}, {"c2", "e1", false},
		} {
			got, err := f.Inbox.Claim(ctx, c.consumer, c.id)
			noErr(t, err)
			if got != c.want {
				t.Fatalf("Claim(%s,%s) = %v, want %v", c.consumer, c.id, got, c.want)
			}
		}
	}},
	{"a claim rolls back with its unit", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		boom := errors.New("boom")
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			if ok, err := f.Inbox.Claim(ctx, "c", "e"); err != nil || !ok {
				t.Fatalf("claim in unit = %v %v", ok, err)
			}
			return boom
		})
		wantErr(t, err, boom)
		ok, err := f.Inbox.Claim(ctx, "c", "e")
		noErr(t, err)
		if !ok {
			t.Fatal("the rolled back claim must be free again")
		}
	}},
}
