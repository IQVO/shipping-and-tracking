package repocontract

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

var snapshotCases = []named{
	{"put then has", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := NewPair(t, 1)
		noErr(t, f.Labels.Save(ctx, p.Label))
		has, err := f.Snapshots.Has(ctx, p.Label.Ref())
		noErr(t, err)
		if has {
			t.Fatal("no snapshot yet")
		}
		noErr(t, f.Snapshots.Put(ctx, p.Label.Ref(), repository.SealedShipTo{KeyID: "k", Nonce: []byte{9}, Ciphertext: []byte{8, 7}}))
		has, err = f.Snapshots.Has(ctx, p.Label.Ref())
		noErr(t, err)
		if !has {
			t.Fatal("snapshot must be stored")
		}
	}},
	{"a snapshot is written once", func(t *testing.T, f Fixture) {
		p := Seed(t, f, 1)
		err := f.Snapshots.Put(context.Background(), p.Label.Ref(), repository.SealedShipTo{KeyID: "k", Nonce: []byte{1}, Ciphertext: []byte{2}})
		wantErr(t, err, ports.ErrConcurrentUpdate)
	}},
	{"purge deletes only snapshots closed strictly before the cutoff", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		closed := Seed(t, f, 1)
		noErr(t, closed.Journey.Void(Hour(5)))
		noErr(t, f.Journeys.Save(ctx, closed.Journey))
		open := Seed(t, f, 2)
		n, err := f.Snapshots.PurgeClosedBefore(ctx, Hour(5))
		noErr(t, err)
		if n != 0 {
			t.Fatalf("a journey closed exactly at the cutoff is kept, purged %d", n)
		}
		n, err = f.Snapshots.PurgeClosedBefore(ctx, Hour(5).Add(1000))
		noErr(t, err)
		if n != 1 {
			t.Fatalf("purged %d, want 1", n)
		}
		assertHas(t, f, closed.Label.Ref(), false)
		assertHas(t, f, open.Label.Ref(), true)
		n, err = f.Snapshots.PurgeClosedBefore(ctx, Hour(500))
		noErr(t, err)
		if n != 0 {
			t.Fatalf("an open journey's snapshot is never purged, purged %d", n)
		}
	}},
}

func assertHas(t *testing.T, f Fixture, ref label.Ref, want bool) {
	t.Helper()
	got, err := f.Snapshots.Has(context.Background(), ref)
	noErr(t, err)
	if got != want {
		t.Fatalf("Has(%s) = %v, want %v", ref, got, want)
	}
}
