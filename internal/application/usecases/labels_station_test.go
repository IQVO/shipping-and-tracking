package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// stationRig is the rig with the local area view (ADR 0019).
func stationRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.deps.AreaModes = r.store.Areas()
	return r
}

func putArea(t *testing.T, r *rig, prefix, station string) {
	t.Helper()
	stored, err := r.deps.AreaModes.Apply(context.Background(), repository.AreaMode{
		PostalPrefix: prefix, DeliveryMode: repository.DeliveryModeNetworkRoute, Version: 1, DeliveryStationNodeID: station})
	if err != nil || !stored {
		t.Fatalf("seed area: stored=%v err=%v", stored, err)
	}
}

func issuedEvent(t *testing.T, r *rig) label.LabelIssued {
	t.Helper()
	evs := r.store.Outbox().Events()
	if len(evs) != 1 {
		t.Fatalf("published %d events, want 1", len(evs))
	}
	return evs[0].(label.LabelIssued)
}

func TestIssueLabelCarriesTheStationOfTheAreaOfTheAddressPrefix(t *testing.T) {
	r := stationRig(t)
	putArea(t, r, "01310", "node-ds-07") // the sample address's prefix
	putArea(t, r, "99999", "node-ds-99")
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	if got := issuedEvent(t, r).DestinationStationNodeID; got != "node-ds-07" {
		t.Fatalf("destination station = %q, want node-ds-07", got)
	}
}

func TestIssueLabelOmitsTheStationWhenNoAreaCoversThePrefix(t *testing.T) {
	r := stationRig(t)
	putArea(t, r, "99999", "node-ds-99") // another prefix
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	if got := issuedEvent(t, r).DestinationStationNodeID; got != "" {
		t.Fatalf("destination station = %q, want none", got)
	}
}

func TestIssueLabelOmitsTheStationOfAnAreaThatNamesNone(t *testing.T) {
	r := stationRig(t)
	putArea(t, r, "01310", "")
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	if got := issuedEvent(t, r).DestinationStationNodeID; got != "" {
		t.Fatalf("destination station = %q, want none", got)
	}
}

func TestIssueLabelWithoutAnAreaViewIssuesWithoutAStation(t *testing.T) {
	r := newRig(t) // Deps.AreaModes is nil
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	noErr(t, err)
	if got := issuedEvent(t, r).DestinationStationNodeID; got != "" {
		t.Fatalf("destination station = %q, want none", got)
	}
}

// brokenAreas is an area view whose read fails.
type brokenAreas struct{ err error }

func (b brokenAreas) Apply(context.Context, repository.AreaMode) (bool, error) { return false, b.err }
func (b brokenAreas) Get(context.Context, string) (repository.AreaMode, error) {
	return repository.AreaMode{}, b.err
}

func TestIssueLabelFailsWithTheAreaViewWhenItIsDownAndStoresNothing(t *testing.T) {
	r := newRig(t)
	boom := errors.New("area view down")
	r.deps.AreaModes = brokenAreas{err: boom}
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-1", 1))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the area view's failure", err)
	}
	if c := r.store.Counts(); c.Labels+c.Journeys+c.OutboxEvents != 0 {
		t.Fatalf("counts = %+v, want nothing stored", c)
	}
}

func TestIssuePickupLabelCarriesTheAreaStationOfThePointsPrefix(t *testing.T) {
	r := pickupRig(t)
	r.deps.AreaModes = r.store.Areas()
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE") // prefix 07095, its serving station node-ds-07
	putArea(t, r, "07095", "node-ds-12")          // the area's station differs: the area wins (ADR 0019)
	pickupOrder(t, r, "node-pnt-07")
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	noErr(t, err)
	if got := issuedEvent(t, r).DestinationStationNodeID; got != "node-ds-12" {
		t.Fatalf("destination station = %q, want the area's node-ds-12", got)
	}
}

func TestIssuePickupLabelOmitsTheStationWhenItsPrefixHasNoArea(t *testing.T) {
	r := pickupRig(t)
	r.deps.AreaModes = r.store.Areas()
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE")
	pickupOrder(t, r, "node-pnt-07")
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	noErr(t, err)
	if got := issuedEvent(t, r).DestinationStationNodeID; got != "" {
		t.Fatalf("destination station = %q, want none (the point's own serving station is not used)", got)
	}
}

func TestIssuePickupLabelFailsWithTheAreaViewWhenItIsDown(t *testing.T) {
	r := pickupRig(t)
	boom := errors.New("area view down")
	r.deps.AreaModes = brokenAreas{err: boom}
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE")
	pickupOrder(t, r, "node-pnt-07")
	_, err := usecases.NewIssueLabel(r.deps).Execute(context.Background(), issueCmd("PO-9", 1))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the area view's failure", err)
	}
}
