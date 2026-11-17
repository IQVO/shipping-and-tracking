package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

var journeyCases = []named{
	{"save then get round-trips scans in recorded order", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		Scan(t, p.Journey, "a", journey.ScanPickupDeparture, 1)
		noErr(t, f.Journeys.Save(ctx, p.Journey))
		Scan(t, p.Journey, "b", journey.ScanDeparture, 2)
		noErr(t, f.Journeys.Save(ctx, p.Journey))
		_, err := p.Journey.RecordScan(journey.ScanInput{ID: "old", NodeID: "n", Type: journey.ScanTransitArrival, At: Hour(0)}, Hour(3))
		wantErr(t, err, journey.ErrScanOutOfOrder)
		noErr(t, f.Journeys.Save(ctx, p.Journey))
		_, err = p.Journey.RaiseException(Hour(40))
		noErr(t, err)
		noErr(t, f.Journeys.Save(ctx, p.Journey))
		got, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if !reflect.DeepEqual(got.Snapshot(), p.Journey.Snapshot()) {
			t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), p.Journey.Snapshot())
		}
		scans := got.Scans()
		if len(scans) != 3 || scans[0].ID != "a" || scans[1].ID != "b" || scans[2].ID != "old" || scans[2].Applied {
			t.Fatalf("scans = %+v", scans)
		}
	}},
	{"get of an unknown journey is ErrNotFound", func(t *testing.T, f Fixture) {
		_, err := f.Journeys.Get(context.Background(), "RNNOPE")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"saving a journey twice at version 1 is ErrConcurrentUpdate", func(t *testing.T, f Fixture) {
		p := Seed(t, f, 1)
		wantErr(t, f.Journeys.Save(context.Background(), p.Journey), ports.ErrConcurrentUpdate)
	}},
	{"later saves append only the new scans", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		for i, typ := range []journey.ScanType{journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival} {
			j, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
			noErr(t, err)
			Scan(t, j, string(rune('a'+i)), typ, i+1)
			noErr(t, f.Journeys.Save(ctx, j))
		}
		got, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if len(got.Scans()) != 3 || got.State() != journey.StateAtStation || got.Version() != 4 {
			t.Fatalf("scans=%d state=%s v%d", len(got.Scans()), got.State(), got.Version())
		}
	}},
	{"a stale journey save is ErrConcurrentUpdate and changes nothing", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		a, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		b, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		Scan(t, a, "a", journey.ScanPickupDeparture, 1)
		noErr(t, f.Journeys.Save(ctx, a))
		Scan(t, b, "b", journey.ScanPickupDeparture, 1)
		wantErr(t, f.Journeys.Save(ctx, b), ports.ErrConcurrentUpdate)
		got, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if len(got.Scans()) != 1 || got.Scans()[0].ID != "a" {
			t.Fatalf("scans = %+v", got.Scans())
		}
	}},
	{"a closed journey keeps its closing fields", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		noErr(t, p.Journey.Void(Hour(3)))
		noErr(t, f.Journeys.Save(ctx, p.Journey))
		got, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if got.State() != journey.StateVoided || !got.ClosedAt().Equal(Hour(3)) {
			t.Fatalf("state=%s closedAt=%v", got.State(), got.ClosedAt())
		}
	}},
}
