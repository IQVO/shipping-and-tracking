package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// testAssignmentID is the shared assignment every label fixture hangs off.
const testAssignmentID = "asg-1"

// testOrderID is the shared order every label fixture hangs off.
const testOrderID = "ord-1"

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
			AssignmentID: testAssignmentID, OrderID: testOrderID, Tier: shared.TierNextDay, StopKey: p.Label.StopKey(),
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
			AssignmentID: testAssignmentID, OrderID: testOrderID, Tier: shared.TierNextDay, StopKey: p.Label.StopKey(),
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
			AssignmentID: testAssignmentID, OrderID: testOrderID, Tier: shared.TierNextDay, StopKey: p.Label.StopKey(),
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
	{"a return label round-trips purpose and authorization (ADR 0017)", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		l := NewReturnLabel(t, 1)
		noErr(t, f.Labels.Save(ctx, l))
		got, err := f.Labels.Get(ctx, l.Ref())
		noErr(t, err)
		if !reflect.DeepEqual(got.Snapshot(), l.Snapshot()) {
			t.Fatalf("round trip differs:\n%+v\n%+v", got.Snapshot(), l.Snapshot())
		}
		if got.Purpose() != label.PurposeReturn || got.ReturnAuthorizationID() != "ret-1" || got.Carrier() != label.CarrierGround {
			t.Fatalf("return label = %s/%s/%s", got.Purpose(), got.ReturnAuthorizationID(), got.Carrier())
		}
	}},
	{"find by return package returns the label, a miss is ErrNotFound", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		l := NewReturnLabel(t, 1)
		noErr(t, f.Labels.Save(ctx, l))
		got, err := f.Labels.FindByReturnPackage(ctx, "ret-1", 1)
		noErr(t, err)
		if got.Ref() != l.Ref() {
			t.Fatalf("found %s", got.Ref())
		}
		_, err = f.Labels.FindByReturnPackage(ctx, "ret-1", 2)
		wantErr(t, err, ports.ErrNotFound)
		_, err = f.Labels.FindByReturnPackage(ctx, "ret-9", 1)
		wantErr(t, err, ports.ErrNotFound)
		// An outbound label is never found by the return lookup.
		p := Seed(t, f, 1)
		_, err = f.Labels.FindByReturnPackage(ctx, p.Label.PONumber(), 1)
		wantErr(t, err, ports.ErrNotFound)
	}},
	{"a second return label for one (authorization, packageNo) is ErrLabelAlreadyIssued", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		l := NewReturnLabel(t, 1)
		noErr(t, f.Labels.Save(ctx, l))
		twin, _, err := label.Issue(label.NewLabel{
			Ref: "lbl-ret-twin", TrackingNumber: "RNRETTWIN0000001", PackageNo: 1, OrderID: testOrderID,
			Purpose: label.PurposeReturn, ReturnAuthorizationID: l.ReturnAuthorizationID(), Tier: shared.TierStandard,
			StopKey: l.StopKey(), DestinationPostalPrefix: "01310",
		}, T0)
		noErr(t, err)
		wantErr(t, f.Labels.Save(ctx, twin), label.ErrLabelAlreadyIssued)
		// A second package of the same authorization is a different label.
		second, _, err := label.Issue(label.NewLabel{
			Ref: "lbl-ret-second", TrackingNumber: "RNRETSECOND00001", PackageNo: 2, OrderID: testOrderID,
			Purpose: label.PurposeReturn, ReturnAuthorizationID: l.ReturnAuthorizationID(), Tier: shared.TierStandard,
			StopKey: l.StopKey(), DestinationPostalPrefix: "01310",
		}, T0)
		noErr(t, err)
		noErr(t, f.Labels.Save(ctx, second))
		// A return of another authorization shares no identity with this one.
		other := NewReturnLabel(t, 2)
		noErr(t, f.Labels.Save(ctx, other))
	}},
	{"a voided return label keeps its purpose", func(t *testing.T, f Fixture) {
		ctx := context.Background()
		l := NewReturnLabel(t, 1)
		noErr(t, f.Labels.Save(ctx, l))
		_, err := l.Void("never shipped", Hour(1))
		noErr(t, err)
		noErr(t, f.Labels.Save(ctx, l))
		got, err := f.Labels.Get(ctx, l.Ref())
		noErr(t, err)
		if got.Status() != label.StatusVoided || got.Purpose() != label.PurposeReturn {
			t.Fatalf("voided return label = %s/%s", got.Status(), got.Purpose())
		}
	}},
}
