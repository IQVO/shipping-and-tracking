package kafka_test

import (
	"context"
	"errors"
	"testing"
	"time"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// ntPickupRegistered is node-topology's own NodeRegistered example (IQVO/
// node-topology apis/asyncapi.yaml on origin/develop, components.messages.
// NodeRegistered, example "nodeRegistered") with the THREE fields in which a
// PICKUP_POINT registration differs from that FULFILLMENT_CENTER one, as the
// producer's schema NodeRegisteredEvent defines them: nodeType PICKUP_POINT,
// subtype null, servingStationNodeId set. The producer publishes no pickup
// example; everything else, the id and the postal code included, is verbatim.
// The three status facts of the same node (ntNodeActivated, ntNodeSuspended,
// ntNodeRetired in handlers_nodetopology_test.go) are verbatim examples.
const ntPickupRegistered = `{"specversion": "1.0", "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40", "source": "/retailnetwork/node-topology", "type": "com.retailnetwork.logistics.node-topology.node.NodeRegistered", "subject": "node-0f3a91", "time": "2026-10-22T13:00:00Z", "datacontenttype": "application/json", "dataschema": "urn:retailnetwork:node-topology:events:NodeRegistered:v1", "data": {"nodeId": "node-0f3a91", "nodeType": "PICKUP_POINT", "subtype": null, "operator": "VENDOR", "vendorId": "vendor-77", "vendorNodeCode": "SP-CAJAMAR-01", "servingStationNodeId": "node-ds-07", "regionId": "region-se", "timezone": "America/Sao_Paulo", "postalCode": "07750-000", "countryCode": "BR"}}`

const (
	nodeActivatedType = "com.retailnetwork.logistics.node-topology.node.NodeActivated"
	examplePoint      = "node-0f3a91" // the node of the producer's examples
	exampleStation    = "node-ds-07"
	examplePostalCode = "07750-000"
)

func (r *nodeTopologyRig) point(t *testing.T, id string) repository.PickupPoint {
	t.Helper()
	p, err := r.store.Points().Get(context.Background(), id)
	if err != nil {
		t.Fatalf("pickup point %s: %v", id, err)
	}
	return p
}

func wantPoint(t *testing.T, when string, got repository.PickupPoint, state string) {
	t.Helper()
	want := repository.PickupPoint{ID: examplePoint, StationNodeID: exampleStation, PostalPrefix: examplePostalCode, State: state}
	if got != want {
		t.Fatalf("%s: point = %+v, want %+v (a status fact changes the state and nothing else)", when, got, want)
	}
}

// Defect 1: a status fact is a lifecycle-only update of an existing pickup
// point. It must leave the serving station and the prefix alone.
func TestAStatusFactChangesOnlyTheStateOfAPickupPoint(t *testing.T) {
	r := newNodeTopologyRig(t)
	r.handle(t, ntPickupRegistered)
	wantPoint(t, "after NodeRegistered", r.point(t, examplePoint), "PROPOSED")
	for _, fact := range []struct{ name, msg, state string }{
		{"NodeActivated", ntNodeActivated, "ACTIVE"}, {"NodeSuspended", ntNodeSuspended, "SUSPENDED"},
		{"NodeRetired", ntNodeRetired, "RETIRED"},
	} {
		r.handle(t, fact.msg)
		wantPoint(t, "after "+fact.name, r.point(t, examplePoint), fact.state)
	}
}

// RETIRED is terminal for node-topology, so it is terminal here: the row is
// kept as RETIRED and no later status fact (a redelivery, a replay) revives it.
func TestARetiredPickupPointStaysRetired(t *testing.T) {
	r := newNodeTopologyRig(t)
	for _, msg := range []string{ntPickupRegistered, ntNodeActivated, ntNodeRetired} {
		r.handle(t, msg)
	}
	r.handle(t, string(envelope(t, "ev-late-activation", nodeActivatedType, `{"nodeId":"node-0f3a91","activatedAt":"2026-12-01T00:00:00Z"}`)))
	wantPoint(t, "an activation after the retirement", r.point(t, examplePoint), "RETIRED")
}

// A status fact of a node that is not a pickup point - the view holds no row
// of it - is acknowledged and makes no pickup-point row. The registration of
// a node precedes its status facts on the partition (both are keyed by the
// node id), so "no row" means "not a pickup point", never "not yet".
func TestAStatusFactOfANodeThatIsNotAPickupPointMakesNoRow(t *testing.T) {
	for name, registration := range map[string]string{"a registered FULFILLMENT_CENTER": ntNodeRegistered, "a node never registered here": ""} {
		t.Run(name, func(t *testing.T) {
			r := newNodeTopologyRig(t)
			if registration != "" {
				r.handle(t, registration)
			}
			for _, msg := range []string{ntNodeActivated, ntNodeSuspended, ntNodeRetired} {
				r.handle(t, msg)
				if p, err := r.store.Points().Get(context.Background(), exampleNode); !errors.Is(err, ports.ErrNotFound) {
					t.Fatalf("PickupPointView holds %+v (%v), want no row", p, err)
				}
			}
		})
	}
}

// A NodeRegistered applied again (a rebuild: its inbox claim is gone) restores
// the descriptive data and must not demote the point to PROPOSED.
func TestNodeRegisteredAppliedAgainDoesNotDemoteAPickupPoint(t *testing.T) {
	for _, c := range []struct {
		name, state string
		facts       []string
	}{
		{"an ACTIVE point", "ACTIVE", []string{ntNodeActivated}},
		{"a SUSPENDED point", "SUSPENDED", []string{ntNodeActivated, ntNodeSuspended}},
		{"a RETIRED point", "RETIRED", []string{ntNodeActivated, ntNodeRetired}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newNodeTopologyRig(t)
			r.handle(t, ntPickupRegistered)
			for _, msg := range c.facts {
				r.handle(t, msg)
			}
			r.store.Inbox().Forget(context.Background(), inbound.ConsumerNodeTopology)
			r.handle(t, ntPickupRegistered)
			wantPoint(t, "after the registration was applied again", r.point(t, examplePoint), c.state)
		})
	}
}

// The repair of a row the defect blanked (runbook, "Repairing the pickup
// point view"). The pickup-point fold claims a NodeRegistered under the shared
// inbox identity "node-topology". While that claim stands a replayed
// registration is SKIPPED and the row stays blank - a new consumer group
// alone repairs nothing. Once the claims of that identity are cleared the
// replay restores station and prefix and keeps the state.
func TestAReplayedRegistrationRepairsABlankedRowOnlyOnceItsClaimIsCleared(t *testing.T) {
	ctx := context.Background()
	r := newNodeTopologyRig(t)
	r.handle(t, ntPickupRegistered)
	r.handle(t, ntNodeActivated)
	// The row as the defect left it: ACTIVE, station and prefix blank.
	if err := r.store.Points().Register(ctx, repository.PickupPoint{ID: examplePoint}); err != nil {
		t.Fatal(err)
	}
	blank := repository.PickupPoint{ID: examplePoint, State: "ACTIVE"}
	if got := r.point(t, examplePoint); got != blank {
		t.Fatalf("damaged row = %+v, want %+v", got, blank)
	}
	r.handle(t, ntPickupRegistered) // replayed under a new consumer group
	if got := r.point(t, examplePoint); got != blank {
		t.Fatalf("a replay under a standing claim changed the row to %+v, want it skipped", got)
	}
	r.store.Inbox().Forget(ctx, inbound.ConsumerNodeTopology)
	for _, msg := range []string{ntPickupRegistered, ntNodeActivated} {
		r.handle(t, msg)
	}
	wantPoint(t, "after the replay with the claims cleared", r.point(t, examplePoint), "ACTIVE")
	if prefix := r.prefix(t, examplePoint); prefix != "07750" {
		t.Fatalf("NodeDestinationView prefix = %q, want it untouched by the pickup repair", prefix)
	}
}

// What the defect did to labels, and what a label gets now: a forward label
// to a pickup point that was registered and activated through the consumer.
// The registration is this repo's own consumed example (apis/asyncapi.yaml,
// message NodeRegistered, example "nodeRegistered": node-pnt-07, 07095000).
func TestAPickupLabelIsIssuedForAPointActivatedByItsFacts(t *testing.T) {
	r := newNodeTopologyRig(t)
	r.handle(t, string(envelope(t, "ev-registered", nodeRegisteredType,
		`{"nodeId":"node-pnt-07","nodeType":"PICKUP_POINT","operator":"PARTNER","servingStationNodeId":"node-ds-07","regionId":"reg-1","timezone":"America/Sao_Paulo","postalCode":"07095000","countryCode":"BR"}`)))
	r.handle(t, string(envelope(t, "ev-activated", nodeActivatedType, `{"nodeId":"node-pnt-07","activatedAt":"2026-11-13T08:00:00Z"}`)))

	gateway := memory.NewShipToGateway()
	gateway.Put("ord-1", shipto.Address{DestinationKind: "PICKUP_POINT", PickupPointID: "node-pnt-07"})
	s := r.store
	issue := usecases.NewIssueLabel(usecases.Deps{
		Labels: s.Labels(), Snapshots: s.Snapshots(), Journeys: s.Journeys(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Clock: memory.NewClock(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)), IDs: &memory.SequentialIDs{}, ShipTo: gateway,
		Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{}, PickupPoints: s.Points(),
	})
	v, err := issue.Execute(context.Background(), usecases.IssueLabelCmd{
		PONumber: "PO-9", PackageNo: 1, AssignmentID: "asg-1", OrderID: "ord-1", Tier: shared.TierNextDay,
	})
	if err != nil {
		t.Fatalf("IssueLabel for an ACTIVE pickup point: %v", err)
	}
	stored, err := s.Labels().Get(context.Background(), label.Ref(v.LabelRef))
	if err != nil {
		t.Fatal(err)
	}
	if stored.DestinationPostalPrefix() != "07095000" {
		t.Fatalf("label prefix %q, want the point's own 07095000", stored.DestinationPostalPrefix())
	}
}
