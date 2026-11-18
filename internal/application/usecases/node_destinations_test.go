package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
)

// The inbox identities the node-topology consumer claims under: the folds that
// existed before ADR 0018 share one, the NodeDestinationView fold has its own.
const (
	inboxNodeTopology     = "node-topology"
	inboxNodeDestinations = "node-topology.node-destinations"
)

func noteNode(t *testing.T, r *rig, eventID, nodeID, postalCode string) {
	t.Helper()
	noErr(t, usecases.NewNoteNodeDestination(r.deps).Execute(context.Background(), usecases.NoteNodeDestinationCmd{
		Consumer: inboxNodeDestinations, EventID: eventID, NodeID: nodeID, PostalCode: postalCode,
	}))
}

func nodePrefix(t *testing.T, r *rig, nodeID string) string {
	t.Helper()
	n, err := r.store.NodeDestinations().Get(context.Background(), nodeID)
	noErr(t, err)
	return n.PostalPrefix
}

func TestNoteNodeDestinationKeepsTheDerivedPrefix(t *testing.T) {
	r := newRig(t)
	noteNode(t, r, "ev-1", "node-fc-01", "07750-000")
	if got := nodePrefix(t, r, "node-fc-01"); got != "07750" {
		t.Fatalf("prefix = %q, want the first five of the registered postal code", got)
	}
	// Only the prefix is kept, never the registered postal code itself.
	n, err := r.store.NodeDestinations().Get(context.Background(), "node-fc-01")
	noErr(t, err)
	if n != (repository.NodeDestination{NodeID: "node-fc-01", PostalPrefix: "07750"}) {
		t.Fatalf("row = %+v", n)
	}
}

// A postal code that leaves no letter or digit from A-Z 0-9 still makes the
// node KNOWN, with an empty prefix: the label use case tells "not consumed
// yet" from "consumed, unusable" in its refusal detail.
func TestNoteNodeDestinationKeepsANodeWhosePostalCodeLeavesNothing(t *testing.T) {
	r := newRig(t)
	noteNode(t, r, "ev-1", "node-fc-02", " - ")
	if got := nodePrefix(t, r, "node-fc-02"); got != "" {
		t.Fatalf("prefix = %q, want none", got)
	}
}

// What the inbox does to a redelivered id: the first delivery claims
// (consumer, id) in the transaction of the upsert, every later delivery of
// that id under the same identity is acknowledged without an effect.
func TestNoteNodeDestinationSkipsARedeliveredEventID(t *testing.T) {
	r := newRig(t)
	noteNode(t, r, "ev-1", "node-fc-01", "07750-000")
	noteNode(t, r, "ev-1", "node-fc-01", "99999-999") // same id: skipped, whatever it carries
	if got := nodePrefix(t, r, "node-fc-01"); got != "07750" {
		t.Fatalf("prefix = %q: a redelivered id must not be applied twice", got)
	}
	noteNode(t, r, "ev-2", "node-fc-01", "99999-999") // another id: applied
	if got := nodePrefix(t, r, "node-fc-01"); got != "99999" {
		t.Fatalf("prefix = %q: a new id upserts the row", got)
	}
}

// The rebuild of ADR 0018 depends on this: an id the pickup-point fold claimed
// long ago (identity node-topology) is still NEW to the NodeDestinationView
// fold (its own identity), so replaying the topic fills the new view, while
// the old claim keeps the replayed NodeRegistered from demoting the pickup
// point back to PROPOSED.
func TestAReplayedNodeRegisteredFillsTheNodeViewAndLeavesThePickupPointAlone(t *testing.T) {
	r := pickupRig(t)
	registered := usecases.NotePointCmd{Consumer: inboxNodeTopology, EventID: "ev-registered",
		Point: repository.PickupPoint{ID: "node-pnt-07", StationNodeID: "node-ds-07", PostalPrefix: "07095000", State: "PROPOSED"}}
	notePoint := usecases.NewNotePickupPoint(r.deps)
	noErr(t, notePoint.Execute(context.Background(), registered))
	putPickupPoint(t, r, "node-pnt-07", "ACTIVE") // the point was activated since

	// The replay: the same id is delivered again to both folds.
	noErr(t, notePoint.Execute(context.Background(), registered))
	noteNode(t, r, "ev-registered", "node-pnt-07", "07095000")

	p, err := r.deps.PickupPoints.Get(context.Background(), "node-pnt-07")
	noErr(t, err)
	if p.State != "ACTIVE" {
		t.Fatalf("pickup point state = %s: the replayed NodeRegistered must be skipped by the old claim", p.State)
	}
	if got := nodePrefix(t, r, "node-pnt-07"); got != "07095" {
		t.Fatalf("node prefix = %q: the replayed NodeRegistered must fill the new view", got)
	}
}

func TestNoteNodeDestinationRollsTheClaimBackWithAFailedUpsert(t *testing.T) {
	r := newRig(t)
	boom := errors.New("database down")
	r.deps.NodeDestinations = failingNodeView{err: boom}
	err := usecases.NewNoteNodeDestination(r.deps).Execute(context.Background(), usecases.NoteNodeDestinationCmd{
		Consumer: inboxNodeDestinations, EventID: "ev-1", NodeID: "node-fc-01", PostalCode: "07750-000",
	})
	wantErr(t, err, boom)
	// The claim rolled back with the unit: the redelivery is applied.
	r.deps.NodeDestinations = r.store.NodeDestinations()
	noteNode(t, r, "ev-1", "node-fc-01", "07750-000")
	if got := nodePrefix(t, r, "node-fc-01"); got != "07750" {
		t.Fatalf("prefix = %q after the retry", got)
	}
}

// failingNodeView is a NodeDestinationView whose every call fails.
type failingNodeView struct{ err error }

var _ ports.NodeDestinationView = failingNodeView{}

func (f failingNodeView) Upsert(context.Context, repository.NodeDestination) error { return f.err }

func (f failingNodeView) Get(context.Context, string) (repository.NodeDestination, error) {
	return repository.NodeDestination{}, f.err
}
