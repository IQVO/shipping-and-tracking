package kafka_test

import (
	"context"
	"errors"
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
)

// The four payloads below are copied VERBATIM (YAML rendered as JSON, same
// keys, same values, same order) from node-topology's contract,
// IQVO/node-topology apis/asyncapi.yaml on origin/develop, components.messages:
//
//	NodeRegistered  example "nodeRegistered"  (a FULFILLMENT_CENTER: not a pickup point)
//	NodeActivated   example "nodeActivated"
//	NodeSuspended   example "nodeSuspended"
//	NodeRetired     example "nodeRetired"
//
// The status facts carry only the nodeId plus an instant or a reason: nothing
// a prefix could be rebuilt from.
const (
	ntNodeRegistered = `{"specversion": "1.0", "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40", "source": "/retailnetwork/node-topology", "type": "com.retailnetwork.logistics.node-topology.node.NodeRegistered", "subject": "node-0f3a91", "time": "2026-10-22T13:00:00Z", "datacontenttype": "application/json", "dataschema": "urn:retailnetwork:node-topology:events:NodeRegistered:v1", "data": {"nodeId": "node-0f3a91", "nodeType": "FULFILLMENT_CENTER", "subtype": "SORTABLE", "operator": "VENDOR", "vendorId": "vendor-77", "vendorNodeCode": "SP-CAJAMAR-01", "regionId": "region-se", "timezone": "America/Sao_Paulo", "postalCode": "07750-000", "countryCode": "BR"}}`
	ntNodeActivated  = `{"specversion": "1.0", "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c45", "source": "/retailnetwork/node-topology", "type": "com.retailnetwork.logistics.node-topology.node.NodeActivated", "subject": "node-0f3a91", "time": "2026-10-22T14:00:00Z", "datacontenttype": "application/json", "dataschema": "urn:retailnetwork:node-topology:events:NodeActivated:v1", "data": {"nodeId": "node-0f3a91", "activatedAt": "2026-10-22T14:00:00Z"}}`
	ntNodeSuspended  = `{"specversion": "1.0", "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c46", "source": "/retailnetwork/node-topology", "type": "com.retailnetwork.logistics.node-topology.node.NodeSuspended", "subject": "node-0f3a91", "time": "2026-10-23T08:30:00Z", "datacontenttype": "application/json", "dataschema": "urn:retailnetwork:node-topology:events:NodeSuspended:v1", "data": {"nodeId": "node-0f3a91", "reason": "carrier strike at the dock", "suspendedAt": "2026-10-23T08:30:00Z"}}`
	ntNodeRetired    = `{"specversion": "1.0", "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c47", "source": "/retailnetwork/node-topology", "type": "com.retailnetwork.logistics.node-topology.node.NodeRetired", "subject": "node-0f3a91", "time": "2026-11-30T23:59:00Z", "datacontenttype": "application/json", "dataschema": "urn:retailnetwork:node-topology:events:NodeRetired:v1", "data": {"nodeId": "node-0f3a91", "retiredAt": "2026-11-30T23:59:00Z"}}`
)

const (
	nodeRegisteredType = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	exampleNode        = "node-0f3a91"
	exampleNodePrefix  = "07750"
)

// nodeTopologyRig is the node-topology consumer over the real use cases and
// the in-memory doubles: what the consumer does is read back from the views.
type nodeTopologyRig struct {
	store    *memory.Store
	consumer *inbound.Consumer
}

func newNodeTopologyRig(t *testing.T) *nodeTopologyRig {
	t.Helper()
	s := memory.NewStore()
	d := usecases.Deps{Inbox: s.Inbox(), UoW: s.UnitOfWork(), AreaModes: s.Areas(), PickupPoints: s.Points(),
		NodeDestinations: s.NodeDestinations()}
	handlers := inbound.NodeTopologyHandlers(usecases.NewApplyAreaAssignment(d), quiet, usecases.NewNotePickupPoint(d),
		usecases.NewNoteNodeDestination(d))
	return &nodeTopologyRig{store: s, consumer: consumerOf(handlers, true)}
}

func (r *nodeTopologyRig) handle(t *testing.T, msg string) {
	t.Helper()
	if err := r.consumer.HandleMessage(context.Background(), []byte(msg)); err != nil {
		t.Fatalf("handle: %v", err)
	}
}

func (r *nodeTopologyRig) prefix(t *testing.T, nodeID string) string {
	t.Helper()
	n, err := r.store.NodeDestinations().Get(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("node %s: %v", nodeID, err)
	}
	return n.PostalPrefix
}

func TestNodeRegisteredOfANonPickupNodeReachesTheNodeDestinationView(t *testing.T) {
	r := newNodeTopologyRig(t)
	r.handle(t, ntNodeRegistered)
	if got := r.prefix(t, exampleNode); got != exampleNodePrefix {
		t.Fatalf("prefix = %q, want %q derived from postalCode 07750-000", got, exampleNodePrefix)
	}
	// It is not a pickup point: the PickupPointView keeps no row of it.
	if _, err := r.store.Points().Get(context.Background(), exampleNode); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("PickupPointView.Get = %v, want ErrNotFound for a FULFILLMENT_CENTER", err)
	}
}

func TestNodeStatusFactsNeverEraseThePrefix(t *testing.T) {
	r := newNodeTopologyRig(t)
	r.handle(t, ntNodeRegistered)
	for _, fact := range []struct{ name, msg string }{
		{"NodeActivated", ntNodeActivated}, {"NodeSuspended", ntNodeSuspended}, {"NodeRetired", ntNodeRetired},
	} {
		r.handle(t, fact.msg)
		if got := r.prefix(t, exampleNode); got != exampleNodePrefix {
			t.Fatalf("after %s the prefix is %q, want %q kept", fact.name, got, exampleNodePrefix)
		}
	}
}

// A status fact of a node whose registration was never consumed makes no row:
// it carries nothing a prefix could be derived from.
func TestNodeStatusFactsAloneMakeNoNodeDestination(t *testing.T) {
	r := newNodeTopologyRig(t)
	for _, msg := range []string{ntNodeActivated, ntNodeSuspended, ntNodeRetired} {
		r.handle(t, msg)
	}
	if _, err := r.store.NodeDestinations().Get(context.Background(), exampleNode); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestNodeRegisteredRedeliveredIsAppliedOnce(t *testing.T) {
	nodes := &fakeNodeNoter{}
	c := consumerOf(inbound.NodeTopologyHandlers(nil, quiet, nil, nodes), true)
	for range 2 {
		if err := c.HandleMessage(context.Background(), []byte(ntNodeRegistered)); err != nil {
			t.Fatal(err)
		}
	}
	// The adapter hands every delivery to the use case with the CloudEvents id
	// under the fold's own inbox identity; the use case's claim dedupes.
	want := usecases.NoteNodeDestinationCmd{Consumer: inbound.ConsumerNodeDestinations,
		EventID: "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40", NodeID: exampleNode, PostalCode: "07750-000"}
	if len(nodes.cmds) != 2 || nodes.cmds[0] != want || nodes.cmds[1] != want {
		t.Fatalf("cmds = %+v, want twice %+v", nodes.cmds, want)
	}
	if inbound.ConsumerNodeDestinations == inbound.ConsumerNodeTopology {
		t.Fatal("the NodeDestinationView fold needs its own inbox identity (ADR 0018: the rebuild by replay)")
	}
}

func TestNodeRegisteredToleratesWhatTheProducerMaySendDifferently(t *testing.T) {
	cases := []struct {
		name, nodeID, data, wantPrefix string
	}{
		{"unknown fields and a pickup point", "node-pnt-07",
			`{"nodeId":"node-pnt-07","nodeType":"PICKUP_POINT","servingStationNodeId":"node-ds-07","postalCode":"07095000","extra":{"a":1}}`, "07095"},
		{"an unknown node type", "node-x", `{"nodeId":"node-x","nodeType":"DRONE_PORT","postalCode":"sw1a 1aa"}`, "SW1A1"},
		{"no postal code at all", "node-x", `{"nodeId":"node-x","nodeType":"SORTATION_CENTER"}`, ""},
		{"a postal code without a letter or digit", "node-x", `{"nodeId":"node-x","nodeType":"SORTATION_CENTER","postalCode":"--"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newNodeTopologyRig(t)
			r.handle(t, string(envelope(t, "ev-1", nodeRegisteredType, c.data)))
			if got := r.prefix(t, c.nodeID); got != c.wantPrefix {
				t.Fatalf("prefix = %q, want %q", got, c.wantPrefix)
			}
		})
	}
}

func TestNodeRegisteredWithoutANodeIDIsDeadLettered(t *testing.T) {
	r := newNodeTopologyRig(t)
	msg := envelope(t, "ev-blank", nodeRegisteredType, `{"nodeType":"FULFILLMENT_CENTER","postalCode":"07750-000"}`)
	if err := r.consumer.HandleMessage(context.Background(), msg); err == nil {
		t.Fatal("a NodeRegistered without a nodeId is a poison message")
	}
}

func TestNodeRegisteredSurfacesAFailedNodeFoldSoTheMessageIsRetried(t *testing.T) {
	boom := errors.New("database down")
	points := &fakePointNoter{}
	c := consumerOf(inbound.NodeTopologyHandlers(nil, quiet, points, &fakeNodeNoter{err: boom}), true)
	if err := c.HandleMessage(context.Background(), []byte(ntNodeRegistered)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the fold's failure", err)
	}
}

type fakeNodeNoter struct {
	cmds []usecases.NoteNodeDestinationCmd
	err  error
}

func (f *fakeNodeNoter) Execute(_ context.Context, cmd usecases.NoteNodeDestinationCmd) error {
	f.cmds = append(f.cmds, cmd)
	return f.err
}

type fakePointNoter struct{ cmds []usecases.NotePointCmd }

func (f *fakePointNoter) Execute(_ context.Context, cmd usecases.NotePointCmd) error {
	f.cmds = append(f.cmds, cmd)
	return nil
}

func (f *fakePointNoter) ExecuteStatus(context.Context, usecases.NotePointStatusCmd) error {
	return nil
}
