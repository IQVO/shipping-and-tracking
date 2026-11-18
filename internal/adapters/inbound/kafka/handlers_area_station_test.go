package kafka_test

import (
	"context"
	"fmt"
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
)

// ntServiceAreaAssigned is copied VERBATIM (YAML rendered as JSON, same keys,
// same values, same order) from node-topology's contract, IQVO/node-topology
// apis/asyncapi.yaml on origin/develop, components.messages.ServiceAreaAssigned,
// example "serviceAreaAssigned": the producer's required deliveryStationNodeId.
const ntServiceAreaAssigned = `{"specversion": "1.0", "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c4a", "source": "/retailnetwork/node-topology", "type": "com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned", "subject": "01310", "time": "2026-10-22T16:00:00Z", "datacontenttype": "application/json", "dataschema": "urn:retailnetwork:node-topology:events:ServiceAreaAssigned:v1", "data": {"postalPrefix": "01310", "deliveryStationNodeId": "node-9b7e02", "regionId": "region-se", "assignmentVersion": 2}}`

// areaEvent is a ServiceAreaAssigned of this shape under the given id; a
// station of "-" leaves deliveryStationNodeId out of the payload.
func areaEvent(id string, version int, station string) string {
	st := ""
	if station != "-" {
		st = fmt.Sprintf(`"deliveryStationNodeId": %q, `, station)
	}
	return `{"specversion": "1.0", "id": "` + id + `", "source": "/retailnetwork/node-topology", "type": "` + inbound.TypeServiceAreaAssigned +
		`", "subject": "01310", "time": "2026-10-22T16:00:00Z", "datacontenttype": "application/json", "data": {"postalPrefix": "01310", ` +
		st + fmt.Sprintf(`"assignmentVersion": %d}}`, version)
}

func (r *nodeTopologyRig) station(t *testing.T) string {
	t.Helper()
	a, err := r.store.Areas().Get(context.Background(), "01310")
	if err != nil {
		t.Fatalf("area 01310: %v", err)
	}
	return a.DeliveryStationNodeID
}

func TestServiceAreaAssignedKeepsTheDeliveryStationOfTheProducersExample(t *testing.T) {
	r := newNodeTopologyRig(t)
	r.handle(t, ntServiceAreaAssigned)
	if got := r.station(t); got != "node-9b7e02" {
		t.Fatalf("station = %q, want node-9b7e02", got)
	}
}

func TestServiceAreaAssignedWithoutAStationKeepsNone(t *testing.T) {
	for _, station := range []string{"-", "", " ", "-bad", "has space"} {
		r := newNodeTopologyRig(t)
		r.handle(t, areaEvent("e-1", 3, station))
		if got := r.station(t); got != "" {
			t.Fatalf("station %q kept as %q, want none (a malformed station is advisory, never a refusal)", station, got)
		}
	}
}

func TestAReplayedAreaEventFillsAMissingStationAndNeverReplacesOne(t *testing.T) {
	r := newNodeTopologyRig(t)
	r.handle(t, areaEvent("e-1", 3, "-"))
	r.handle(t, areaEvent("e-2", 3, "node-ds-07")) // re-emitted at the same version, a new id
	if got := r.station(t); got != "node-ds-07" {
		t.Fatalf("station after the re-emit = %q, want node-ds-07", got)
	}
	r.handle(t, areaEvent("e-3", 3, "node-ds-99")) // an equal version never replaces a station
	if got := r.station(t); got != "node-ds-07" {
		t.Fatalf("station after an equal version with another station = %q, want node-ds-07 kept", got)
	}
	r.handle(t, areaEvent("e-4", 4, "node-ds-99")) // a newer version does
	if got := r.station(t); got != "node-ds-99" {
		t.Fatalf("station after a newer version = %q, want node-ds-99", got)
	}
}

// An id the area fold claimed before the station was kept (under the old
// identity) is still new to the area fold's own identity, so a replay of the
// topic fills the row; the same id delivered twice is applied once.
func TestAnIdClaimedUnderTheOldIdentityStillFillsTheStation(t *testing.T) {
	r := newNodeTopologyRig(t)
	ctx := context.Background()
	if claimed, err := r.store.Inbox().Claim(ctx, inbound.ConsumerNodeTopology, "e-1"); err != nil || !claimed {
		t.Fatalf("claim under the old identity: %v %v", claimed, err)
	}
	r.handle(t, areaEvent("e-0", 3, "-"))
	r.handle(t, areaEvent("e-1", 3, "node-ds-07"))
	if got := r.station(t); got != "node-ds-07" {
		t.Fatalf("station = %q, want node-ds-07 from the replayed id", got)
	}
	if inbound.ConsumerAreaModes == inbound.ConsumerNodeTopology {
		t.Fatal("the area fold must claim under its own identity")
	}
	if claimed, _ := r.store.Inbox().Claim(ctx, inbound.ConsumerAreaModes, "e-1"); claimed {
		t.Fatal("the replayed id must be claimed under the area identity")
	}
}
