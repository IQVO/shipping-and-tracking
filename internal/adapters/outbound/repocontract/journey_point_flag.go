package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

// journeyPointFlagCases: the delivery-point flag of a journey (ADR 0014) is
// persisted with the aggregate and every field of it round-trips.
var journeyPointFlagCases = []named{
	{"a journey without a flag round-trips with both fields zero", func(t *testing.T, f Fixture) {
		p := Seed(t, f, 1)
		got, err := f.Journeys.Get(context.Background(), p.Journey.TrackingNumber())
		noErr(t, err)
		s := got.Snapshot()
		if s.PointFlagReason != "" || !s.PointFlaggedAt.IsZero() {
			t.Fatalf("flag = %q %v", s.PointFlagReason, s.PointFlaggedAt)
		}
	}},
	{"a delivery-point flag round-trips and a newer one replaces it", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		j, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		Scan(t, j, "a", journey.ScanPickupDeparture, 1)
		noErr(t, f.Journeys.Save(ctx, j))
		_, err = j.FlagDeliveryPoint("NOT_HOME", Hour(2), Hour(2))
		noErr(t, err)
		noErr(t, f.Journeys.Save(ctx, j))
		got, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if !reflect.DeepEqual(got.Snapshot(), j.Snapshot()) {
			t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), j.Snapshot())
		}
		if s := got.Snapshot(); s.PointFlagReason != "NOT_HOME" || !s.PointFlaggedAt.Equal(Hour(2)) {
			t.Fatalf("flag = %q %v", s.PointFlagReason, s.PointFlaggedAt)
		}
		_, err = got.FlagDeliveryPoint("ACCESS_PROBLEM", Hour(26), Hour(26))
		noErr(t, err)
		noErr(t, f.Journeys.Save(ctx, got))
		again, err := f.Journeys.Get(ctx, p.Journey.TrackingNumber())
		noErr(t, err)
		if s := again.Snapshot(); s.PointFlagReason != "ACCESS_PROBLEM" || !s.PointFlaggedAt.Equal(Hour(26)) {
			t.Fatalf("flag = %q %v", s.PointFlagReason, s.PointFlaggedAt)
		}
	}},
}
