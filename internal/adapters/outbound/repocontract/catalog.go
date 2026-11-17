package repocontract

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
)

func catalogRow(id string) repository.CatalogDimensions {
	return repository.CatalogDimensions{
		CatalogItemID: id, Title: "Desk lamp", LengthMm: 300, WidthMm: 200, HeightMm: 150, WeightG: 900, Hazmat: false, Fragile: true,
	}
}

var catalogCases = []named{
	{"upsert then get, a second upsert replaces", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		noErr(t, f.Catalog.Upsert(ctx, catalogRow("item-1")))
		got, err := f.Catalog.Get(ctx, "item-1")
		noErr(t, err)
		if got != catalogRow("item-1") {
			t.Fatalf("got %+v", got)
		}
		changed := catalogRow("item-1")
		changed.Title, changed.Hazmat, changed.WeightG = "Desk lamp XL", true, 1200
		noErr(t, f.Catalog.Upsert(ctx, changed))
		got, err = f.Catalog.Get(ctx, "item-1")
		noErr(t, err)
		if got != changed {
			t.Fatalf("got %+v", got)
		}
	}},
	{"get of an unknown item is ErrNotFound", func(t *testing.T, f Fixture) {
		_, err := f.Catalog.Get(context.Background(), "nope")
		wantErr(t, err, ports.ErrNotFound)
	}},
}
