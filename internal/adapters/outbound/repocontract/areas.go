package repocontract

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
)

const areaPrefix = "01310"

func area(version int64, station string) repository.AreaMode {
	return repository.AreaMode{PostalPrefix: areaPrefix, DeliveryMode: repository.DeliveryModeNetworkRoute, Version: version,
		DeliveryStationNodeID: station}
}

func applyArea(t *testing.T, f Fixture, a repository.AreaMode) bool {
	t.Helper()
	stored, err := f.Areas.Apply(context.Background(), a)
	noErr(t, err)
	return stored
}

func wantArea(t *testing.T, f Fixture, want repository.AreaMode) {
	t.Helper()
	got, err := f.Areas.Get(context.Background(), areaPrefix)
	noErr(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("area = %+v, want %+v", got, want)
	}
}

// areaCases is the contract of ports.AreaModeView, the assignment-version
// guard and its station backfill included (ADR 0019).
var areaCases = []named{
	{"apply then get keeps the mode, the carriers, the version and the station", func(t *testing.T, f Fixture) {
		a := repository.AreaMode{PostalPrefix: areaPrefix, DeliveryMode: repository.DeliveryModeCarrierHandoff, Version: 4,
			DeliveryStationNodeID: "node-ds-07", Carriers: []journey.CarrierOption{
				{CarrierID: "crr-fast", HandoffNodeID: "node-sc-03", CostPerPackageCents: 420, TransitMinutes: 1440}}}
		if !applyArea(t, f, a) {
			t.Fatal("the first assignment must be stored")
		}
		wantArea(t, f, a)
	}},
	{"an assignment without a station reads back an empty station", func(t *testing.T, f Fixture) {
		applyArea(t, f, area(2, ""))
		wantArea(t, f, area(2, ""))
	}},
	{"get of an unknown prefix is ErrNotFound", func(t *testing.T, f Fixture) {
		_, err := f.Areas.Get(context.Background(), "99999")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"a newer version replaces the row, its station included, even with none", func(t *testing.T, f Fixture) {
		applyArea(t, f, area(2, "node-ds-07"))
		if !applyArea(t, f, area(3, "node-ds-08")) {
			t.Fatal("a newer version must be stored")
		}
		wantArea(t, f, area(3, "node-ds-08"))
		if !applyArea(t, f, area(4, "")) {
			t.Fatal("a newer version must be stored")
		}
		wantArea(t, f, area(4, ""))
	}},
	{"an older version is dropped, a station included", func(t *testing.T, f Fixture) {
		applyArea(t, f, area(3, ""))
		if applyArea(t, f, area(2, "node-ds-07")) {
			t.Fatal("an older version must be dropped")
		}
		wantArea(t, f, area(3, ""))
	}},
	{"an equal version fills a missing station", func(t *testing.T, f Fixture) {
		applyArea(t, f, area(3, ""))
		if !applyArea(t, f, area(3, "node-ds-07")) {
			t.Fatal("an equal version that brings a station to a row without one must be stored")
		}
		wantArea(t, f, area(3, "node-ds-07"))
	}},
	{"an equal version never replaces a stored station", func(t *testing.T, f Fixture) {
		applyArea(t, f, area(3, "node-ds-07"))
		if applyArea(t, f, area(3, "node-ds-99")) {
			t.Fatal("an equal version must not replace a stored station")
		}
		wantArea(t, f, area(3, "node-ds-07"))
	}},
	{"an equal version without a station changes nothing", func(t *testing.T, f Fixture) {
		applyArea(t, f, area(3, ""))
		if applyArea(t, f, area(3, "")) {
			t.Fatal("an equal version with no station to add must be dropped")
		}
		applyArea(t, f, area(5, "node-ds-07"))
		if applyArea(t, f, area(5, "")) {
			t.Fatal("an equal version with no station must be dropped")
		}
		wantArea(t, f, area(5, "node-ds-07"))
	}},
	{"an apply rolls back with its unit", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		boom := errors.New("boom")
		err := f.UoW.Do(ctx, func(ctx context.Context) error {
			if _, err := f.Areas.Apply(ctx, area(2, "node-ds-07")); err != nil {
				return err
			}
			return boom
		})
		wantErr(t, err, boom)
		_, err = f.Areas.Get(ctx, areaPrefix)
		wantErr(t, err, ports.ErrNotFound)
	}},
}
