package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// fulfillmentNode is a node that is not a pickup point.
const fulfillmentNode = "node-fc-01"

// nodeDestinationCases is the contract of ports.NodeDestinationView (ADR 0018).
var nodeDestinationCases = []named{
	{"upsert then get, a second upsert replaces the prefix", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		first := repository.NodeDestination{NodeID: fulfillmentNode, PostalPrefix: "07750"}
		noErr(t, f.NodeDestinations.Upsert(ctx, first))
		got, err := f.NodeDestinations.Get(ctx, fulfillmentNode)
		noErr(t, err)
		if got != first {
			t.Fatalf("got %+v, want %+v", got, first)
		}
		second := repository.NodeDestination{NodeID: fulfillmentNode, PostalPrefix: "SW1A1"}
		noErr(t, f.NodeDestinations.Upsert(ctx, second))
		got, err = f.NodeDestinations.Get(ctx, fulfillmentNode)
		noErr(t, err)
		if got != second {
			t.Fatalf("got %+v, want %+v", got, second)
		}
	}},
	{"nodes are kept apart by id", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		noErr(t, f.NodeDestinations.Upsert(ctx, repository.NodeDestination{NodeID: "node-a", PostalPrefix: "11111"}))
		noErr(t, f.NodeDestinations.Upsert(ctx, repository.NodeDestination{NodeID: "node-b", PostalPrefix: "22222"}))
		for id, want := range map[string]string{"node-a": "11111", "node-b": "22222"} {
			got, err := f.NodeDestinations.Get(ctx, id)
			noErr(t, err)
			if got.NodeID != id || got.PostalPrefix != want {
				t.Fatalf("Get(%s) = %+v, want prefix %s", id, got, want)
			}
		}
	}},
	{"a known node may have no usable prefix", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		noErr(t, f.NodeDestinations.Upsert(ctx, repository.NodeDestination{NodeID: "node-fc-02"}))
		got, err := f.NodeDestinations.Get(ctx, "node-fc-02")
		noErr(t, err)
		if got != (repository.NodeDestination{NodeID: "node-fc-02"}) {
			t.Fatalf("got %+v, want the node with an empty prefix", got)
		}
	}},
	{"get of an unknown node is ErrNotFound", func(t *testing.T, f Fixture) {
		_, err := f.NodeDestinations.Get(context.Background(), "nope")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"a blank node id is refused and stores nothing", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		if err := f.NodeDestinations.Upsert(ctx, repository.NodeDestination{PostalPrefix: "07750"}); err == nil {
			t.Fatal("an upsert without a node id must fail")
		}
		_, err := f.NodeDestinations.Get(ctx, "")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"an upsert rolls back with its unit", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		boom := errors.New("boom")
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			noErr(t, f.NodeDestinations.Upsert(ctx, repository.NodeDestination{NodeID: fulfillmentNode, PostalPrefix: "07750"}))
			return boom
		})
		wantErr(t, err, boom)
		_, err = f.NodeDestinations.Get(ctx, fulfillmentNode)
		wantErr(t, err, ports.ErrNotFound)
	}},
}
