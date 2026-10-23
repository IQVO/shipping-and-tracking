package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func TestRecordCatalogItemClaimsThenUpserts(t *testing.T) {
	r := newRig(t)
	row := repository.CatalogDimensions{CatalogItemID: "item-1", Title: "Lamp", LengthMm: 300, WidthMm: 200, HeightMm: 150, WeightG: 900, Fragile: true}
	uc := usecases.NewRecordCatalogItem(r.deps)
	ok, err := uc.Execute(context.Background(), "catalog", "ev-1", row)
	noErr(t, err)
	if !ok {
		t.Fatal("first delivery is processed")
	}
	got, err := r.store.Catalog().Get(context.Background(), "item-1")
	noErr(t, err)
	if got != row {
		t.Fatalf("stored %+v", got)
	}
	changed := row
	changed.Title = "Other"
	ok, err = uc.Execute(context.Background(), "catalog", "ev-1", changed)
	noErr(t, err)
	if ok {
		t.Fatal("a redelivery is skipped")
	}
	if got, _ := r.store.Catalog().Get(context.Background(), "item-1"); got.Title != "Lamp" {
		t.Fatalf("a skipped event changes nothing, got %+v", got)
	}
	_, err = uc.Execute(context.Background(), "catalog", "ev-2", repository.CatalogDimensions{CatalogItemID: " "})
	wantErr(t, err, shared.ErrValidation)
}

func TestGetJourneyReturnsTheViewOrNotFound(t *testing.T) {
	r := newRig(t)
	tn := r.issued(t)
	v, err := usecases.NewGetJourney(r.deps).Execute(context.Background(), tn)
	noErr(t, err)
	if v.TrackingNumber != string(tn) || v.State != "LABELLED" || v.Scans == nil || len(v.Scans) != 0 {
		t.Fatalf("view = %+v", v)
	}
	_, err = usecases.NewGetJourney(r.deps).Execute(context.Background(), "RNNOPE")
	wantErr(t, err, ports.ErrNotFound)
}
