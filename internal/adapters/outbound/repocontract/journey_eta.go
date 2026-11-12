package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// journeyEtaCases: the ETA window of a journey (ADR 0013) is persisted with the
// aggregate and every field of it round-trips.
var journeyEtaCases = []named{
	{"a journey without an ETA window round-trips with all three fields zero", func(t *testing.T, f Fixture) {
		p := Seed(t, f, 1)
		got, err := f.Journeys.Get(context.Background(), p.Journey.TrackingNumber())
		noErr(t, err)
		s := got.Snapshot()
		if !s.EtaStart.IsZero() || !s.EtaEnd.IsZero() || !s.EtaAssignedAt.IsZero() {
			t.Fatalf("eta = %v %v %v", s.EtaStart, s.EtaEnd, s.EtaAssignedAt)
		}
	}},
	{"an assigned ETA window round-trips and a newer one replaces it", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		j, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		Scan(t, j, "a", journey.ScanPickupDeparture, 1)
		noErr(t, f.Journeys.Save(ctx, j))
		_, err = j.AssignEta(Hour(3), Hour(5), Hour(2), Hour(2))
		noErr(t, err)
		noErr(t, f.Journeys.Save(ctx, j))
		got, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if !reflect.DeepEqual(got.Snapshot(), j.Snapshot()) {
			t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), j.Snapshot())
		}
		if s := got.Snapshot(); !s.EtaStart.Equal(Hour(3)) || !s.EtaEnd.Equal(Hour(5)) || !s.EtaAssignedAt.Equal(Hour(2)) {
			t.Fatalf("eta = %v %v %v", s.EtaStart, s.EtaEnd, s.EtaAssignedAt)
		}
		_, err = got.AssignEta(Hour(30), Hour(32), Hour(26), Hour(26))
		noErr(t, err)
		noErr(t, f.Journeys.Save(ctx, got))
		again, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if s := again.Snapshot(); !s.EtaStart.Equal(Hour(30)) || !s.EtaEnd.Equal(Hour(32)) || !s.EtaAssignedAt.Equal(Hour(26)) || s.Version != got.Version() {
			t.Fatalf("eta = %v %v %v v%d", s.EtaStart, s.EtaEnd, s.EtaAssignedAt, s.Version)
		}
	}},
}
