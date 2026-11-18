package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

// registeredPoint is the pickup point of node-topology's NodeRegistered
// example (IQVO/node-topology apis/asyncapi.yaml on origin/develop,
// components.messages.NodeRegistered, example "nodeRegistered": nodeId and
// postalCode verbatim) as the consumer registers a PICKUP_POINT: PROPOSED,
// with its serving station.
var registeredPoint = repository.PickupPoint{
	ID: "node-0f3a91", StationNodeID: "node-ds-07", PostalPrefix: "07750-000", State: repository.PickupPointProposed,
}

func pointIs(t *testing.T, f Fixture, want repository.PickupPoint) {
	t.Helper()
	got, err := f.PickupPoints.Get(context.Background(), want.ID)
	noErr(t, err)
	if got != want {
		t.Fatalf("point = %+v, want %+v", got, want)
	}
}

func setState(t *testing.T, f Fixture, id, state string, wantChanged bool) {
	t.Helper()
	changed, err := f.PickupPoints.SetState(context.Background(), id, state)
	noErr(t, err)
	if changed != wantChanged {
		t.Fatalf("SetState(%s, %s) changed = %v, want %v", id, state, changed, wantChanged)
	}
}

// pickupPointCases is the contract of ports.PickupPointView (ADR 0016 and its
// addendum of 2026-11-18): a registration writes the descriptive data, a
// status fact writes the state, and neither touches the other's columns.
var pickupPointCases = []named{
	{"a status fact changes the state and leaves station and prefix intact", func(t *testing.T, f Fixture) {
		noErr(t, f.PickupPoints.Register(context.Background(), registeredPoint))
		pointIs(t, f, registeredPoint)
		for _, state := range []string{repository.PickupPointActive, repository.PickupPointSuspended, repository.PickupPointActive} {
			setState(t, f, registeredPoint.ID, state, true)
			want := registeredPoint
			want.State = state
			pointIs(t, f, want)
		}
	}},
	{"a status fact of an id the view does not hold makes no row", func(t *testing.T, f Fixture) {
		for _, state := range []string{repository.PickupPointActive, repository.PickupPointSuspended, repository.PickupPointRetired} {
			setState(t, f, fulfillmentNode, state, false)
			_, err := f.PickupPoints.Get(context.Background(), fulfillmentNode)
			wantErr(t, err, ports.ErrNotFound)
		}
	}},
	{"a retired point keeps its row and its data and is never revived", func(t *testing.T, f Fixture) {
		noErr(t, f.PickupPoints.Register(context.Background(), registeredPoint))
		setState(t, f, registeredPoint.ID, repository.PickupPointActive, true)
		setState(t, f, registeredPoint.ID, repository.PickupPointRetired, true)
		want := registeredPoint
		want.State = repository.PickupPointRetired
		pointIs(t, f, want)
		for _, state := range []string{repository.PickupPointActive, repository.PickupPointSuspended, repository.PickupPointRetired} {
			setState(t, f, registeredPoint.ID, state, false)
			pointIs(t, f, want)
		}
	}},
	{"a registration applied again replaces the data and never the state", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		for _, state := range []string{repository.PickupPointActive, repository.PickupPointSuspended, repository.PickupPointRetired} {
			p := registeredPoint
			p.ID = "node-pnt-" + state
			noErr(t, f.PickupPoints.Register(ctx, p))
			setState(t, f, p.ID, state, true)
			again := p
			again.StationNodeID, again.PostalPrefix = "node-ds-08", "07095000"
			noErr(t, f.PickupPoints.Register(ctx, again)) // again.State is PROPOSED
			again.State = state
			pointIs(t, f, again)
		}
	}},
	{"a registration repairs a blanked row and keeps its state", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		noErr(t, f.PickupPoints.Register(ctx, repository.PickupPoint{ID: registeredPoint.ID, State: repository.PickupPointActive}))
		pointIs(t, f, repository.PickupPoint{ID: registeredPoint.ID, State: repository.PickupPointActive})
		noErr(t, f.PickupPoints.Register(ctx, registeredPoint))
		want := registeredPoint
		want.State = repository.PickupPointActive
		pointIs(t, f, want)
	}},
	{"points are kept apart by id", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		other := repository.PickupPoint{ID: "node-pnt-07", StationNodeID: "node-ds-09", PostalPrefix: "07095000", State: repository.PickupPointProposed}
		noErr(t, f.PickupPoints.Register(ctx, registeredPoint))
		noErr(t, f.PickupPoints.Register(ctx, other))
		setState(t, f, other.ID, repository.PickupPointActive, true)
		pointIs(t, f, registeredPoint)
		other.State = repository.PickupPointActive
		pointIs(t, f, other)
	}},
	{"get of an unknown point is ErrNotFound", func(t *testing.T, f Fixture) {
		_, err := f.PickupPoints.Get(context.Background(), "nope")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"a blank id is refused and stores nothing", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		if err := f.PickupPoints.Register(ctx, repository.PickupPoint{StationNodeID: "node-ds-07", State: repository.PickupPointProposed}); err == nil {
			t.Fatal("a registration without an id must fail")
		}
		setState(t, f, "", repository.PickupPointActive, false)
		_, err := f.PickupPoints.Get(ctx, "")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"both writes roll back with their unit", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		boom := errors.New("boom")
		noErr(t, f.PickupPoints.Register(ctx, registeredPoint))
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			changed, err := f.PickupPoints.SetState(ctx, registeredPoint.ID, repository.PickupPointActive)
			noErr(t, err)
			if !changed {
				t.Fatal("SetState inside the unit must change the row")
			}
			noErr(t, f.PickupPoints.Register(ctx, repository.PickupPoint{ID: "node-pnt-07", State: repository.PickupPointProposed}))
			return boom
		})
		wantErr(t, err, boom)
		pointIs(t, f, registeredPoint)
		_, err = f.PickupPoints.Get(ctx, "node-pnt-07")
		wantErr(t, err, ports.ErrNotFound)
	}},
}
