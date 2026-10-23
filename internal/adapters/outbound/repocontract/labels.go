package repocontract

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var labelCases = []named{
	{"save then get round-trips every field", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := NewPair(t, 1)
		noErr(t, f.Labels.Save(ctx, p.Label))
		got, err := f.Labels.Get(ctx, p.Label.Ref())
		noErr(t, err)
		if !reflect.DeepEqual(got.Snapshot(), p.Label.Snapshot()) {
			t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), p.Label.Snapshot())
		}
	}},
	{"get of an unknown label is ErrNotFound", func(t *testing.T, f Fixture) {
		_, err := f.Labels.Get(context.Background(), "nope")
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"find by package returns the label, a miss is ErrNotFound", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		got, err := f.Labels.FindByPackage(ctx, p.Label.PONumber(), p.Label.PackageNo())
		noErr(t, err)
		if got.Ref() != p.Label.Ref() {
			t.Fatalf("found %s", got.Ref())
		}
		_, err = f.Labels.FindByPackage(ctx, p.Label.PONumber(), 2)
		wantErr(t, err, ports.ErrNotFound)
		_, err = f.Labels.FindByPackage(ctx, "PO-other", 1)
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"a second label for one package is ErrLabelAlreadyIssued", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		twin, _, err := label.Issue(label.NewLabel{
			Ref: "lbl-twin", TrackingNumber: "RNTWIN", PONumber: p.Label.PONumber(), PackageNo: p.Label.PackageNo(),
			AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay, StopKey: p.Label.StopKey(),
			DestinationPostalPrefix: "01310",
		}, T0)
		noErr(t, err)
		wantErr(t, f.Labels.Save(ctx, twin), label.ErrLabelAlreadyIssued)
	}},
	{"a second label with one tracking number is ErrTrackingNumberTaken", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		twin, _, err := label.Issue(label.NewLabel{
			Ref: "lbl-twin", TrackingNumber: p.Label.TrackingNumber(), PONumber: "PO-twin", PackageNo: 1,
			AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay, StopKey: p.Label.StopKey(),
			DestinationPostalPrefix: "01310",
		}, T0)
		noErr(t, err)
		wantErr(t, f.Labels.Save(ctx, twin), label.ErrTrackingNumberTaken)
	}},
	{"another package of the same order is a different label", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		second, _, err := label.Issue(label.NewLabel{
			Ref: "lbl-second", TrackingNumber: "RNSECOND", PONumber: p.Label.PONumber(), PackageNo: 2,
			AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay, StopKey: p.Label.StopKey(),
			DestinationPostalPrefix: "01310",
		}, T0)
		noErr(t, err)
		noErr(t, f.Labels.Save(ctx, second))
	}},
	{"saving a label twice is ErrConcurrentUpdate", func(t *testing.T, f Fixture) {
		p := Seed(t, f, 1)
		wantErr(t, f.Labels.Save(context.Background(), p.Label), ports.ErrConcurrentUpdate)
	}},
	{"an update saves at version+1 and a stale one is ErrConcurrentUpdate", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		p := Seed(t, f, 1)
		stale, err := f.Labels.Get(ctx, p.Label.Ref())
		noErr(t, err)
		fresh, err := f.Labels.Get(ctx, p.Label.Ref())
		noErr(t, err)
		_, err = fresh.Void("gone", Hour(1))
		noErr(t, err)
		noErr(t, f.Labels.Save(ctx, fresh))
		_, err = stale.Void("also gone", Hour(2))
		noErr(t, err)
		wantErr(t, f.Labels.Save(ctx, stale), ports.ErrConcurrentUpdate)
		got, err := f.Labels.Get(ctx, p.Label.Ref())
		noErr(t, err)
		if got.Status() != label.StatusVoided || got.VoidReason() != "gone" || got.Version() != 2 {
			t.Fatalf("stored label = %s %q v%d", got.Status(), got.VoidReason(), got.Version())
		}
	}},
	{"updating a label that was never stored is ErrConcurrentUpdate", func(t *testing.T, f Fixture) {
		p := NewPair(t, 1)
		_, err := p.Label.Void("gone", Hour(1))
		noErr(t, err)
		wantErr(t, f.Labels.Save(context.Background(), p.Label), ports.ErrConcurrentUpdate)
	}},
}

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
