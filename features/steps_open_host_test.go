// Steps for the Open Host journey query (open_host.feature), the label
// collision that survives its retry (issue_label_edges.feature) and the
// arrival/delivery facts of consumed events (consumed_arrivals.feature).
// They reuse the world of features_test.go: the same use cases the REST
// adapter and the Kafka consumers call, over the in-memory doubles.
package features_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// timeFormat renders instants in step failures.
const timeFormat = time.RFC3339

func registerOpenHostSteps(sc *godog.ScenarioContext, w *world) {
	// The Open Host query (GET /journeys/{trackingNumber}).
	sc.Step(`^I query the journey of the package$`, w.queryTheJourney)
	sc.Step(`^I query the journey of tracking number "([^"]*)"$`, w.queryTheJourneyOf)
	sc.Step(`^the timeline state is "([A-Z_]+)"$`, w.timelineStateIs)
	sc.Step(`^the timeline has the scans "([^"]*)" in the order they were recorded$`, w.timelineScansAre)
	sc.Step(`^the timeline has (\d+) scans of which (\d+) applied$`, w.timelineHasScans)
	sc.Step(`^the scan "([^"]*)" is applied$`, func(id string) error { return w.scanApplied(id, true) })
	sc.Step(`^the scan "([^"]*)" is not applied$`, func(id string) error { return w.scanApplied(id, false) })
	sc.Step(`^the timeline says the package was handed over at hour (\d+)$`, w.timelineHandedOverAt)
	sc.Step(`^the timeline says the package was delivered at hour (\d+)$`, w.timelineDeliveredAt)
	sc.Step(`^the last recorded scan has node "([^"]*)"$`, w.lastRecordedScanNodeIs)

	// not-found refusals the REST adapter answers 404 for.
	sc.Step(`^I void the label "([^"]*)" with reason "([^"]*)"$`, w.iVoidTheNamedLabel)
	sc.Step(`^I record a "([A-Z_]+)" scan "([^"]*)" at hour (\d+) on tracking number "([^"]*)"$`, w.iRecordAScanOn)

	// A tracking-number collision that survives the single retry.
	sc.Step(`^the next two tracking numbers generated are already taken$`, func() error {
		w.nextTwoTrackingNumbersAreTaken()
		return nil
	})

	// Arrival and delivery facts of consumed events, seeded through the same
	// use case (ApplyScans) the Kafka consumer calls.
	sc.Step(`^a linehaul event arrives the package at node "([^"]*)" of type "([^"]*)" at hour (\d+)$`, w.linehaulArrives)
	sc.Step(`^a last-mile event delivers the package at hour (\d+) with no node of its own$`, w.lastMileDelivers)
}

// --- the query -------------------------------------------------------------

func (w *world) queryTheJourney() error {
	return w.queryJourney(w.tracking())
}

func (w *world) queryTheJourneyOf(tn string) error {
	return w.queryJourney(shared.TrackingNumber(tn))
}

func (w *world) queryJourney(tn shared.TrackingNumber) error {
	v, err := usecases.NewGetJourney(w.deps).Execute(context.Background(), tn)
	w.lastJourney, w.lastErr = v, err
	return nil
}

// --- timeline assertions ---------------------------------------------------

func (w *world) timelineStateIs(state string) error {
	if w.lastJourney.State != state {
		return fmt.Errorf("timeline state is %s, want %s", w.lastJourney.State, state)
	}
	return nil
}

func (w *world) timelineScansAre(list string) error {
	want := strings.Split(list, ",")
	if len(w.lastJourney.Scans) != len(want) {
		return fmt.Errorf("timeline has %d scans, want %d (%s)", len(w.lastJourney.Scans), len(want), list)
	}
	for i, s := range w.lastJourney.Scans {
		if s.ScanID != want[i] {
			return fmt.Errorf("scan %d of the timeline is %q, want %q", i+1, s.ScanID, want[i])
		}
	}
	return nil
}

func (w *world) scanApplied(id string, applied bool) error {
	for _, s := range w.lastJourney.Scans {
		if s.ScanID == id {
			if s.Applied != applied {
				return fmt.Errorf("scan %q applied=%v, want %v", id, s.Applied, applied)
			}
			return nil
		}
	}
	return fmt.Errorf("scan %q is not in the timeline", id)
}

func (w *world) timelineHasScans(total, applied int) error {
	got := 0
	for _, s := range w.lastJourney.Scans {
		if s.Applied {
			got++
		}
	}
	if len(w.lastJourney.Scans) != total || got != applied {
		return fmt.Errorf("timeline has %d scans of which %d applied, want %d of which %d",
			len(w.lastJourney.Scans), got, total, applied)
	}
	return nil
}

func (w *world) timelineHandedOverAt(n int) error {
	if w.lastJourney.HandedOverAt == nil {
		return fmt.Errorf("the timeline carries no handedOverAt, want hour %d", n)
	}
	if !w.lastJourney.HandedOverAt.Equal(hour(n)) {
		return fmt.Errorf("handedOverAt = %s, want %s", w.lastJourney.HandedOverAt.Format(timeFormat), hour(n).Format(timeFormat))
	}
	return nil
}

func (w *world) timelineDeliveredAt(n int) error {
	if w.lastJourney.DeliveredAt == nil {
		return fmt.Errorf("the timeline carries no deliveredAt, want hour %d", n)
	}
	if !w.lastJourney.DeliveredAt.Equal(hour(n)) {
		return fmt.Errorf("deliveredAt = %s, want %s", w.lastJourney.DeliveredAt.Format(timeFormat), hour(n).Format(timeFormat))
	}
	return nil
}

func (w *world) lastRecordedScanNodeIs(node string) error {
	if len(w.lastJourney.Scans) == 0 {
		return fmt.Errorf("the timeline has no scans, want the last one at node %q", node)
	}
	last := w.lastJourney.Scans[len(w.lastJourney.Scans)-1]
	if last.NodeID != node {
		return fmt.Errorf("the last recorded scan is at node %q, want %q", last.NodeID, node)
	}
	return nil
}

// --- not-found refusals ----------------------------------------------------

func (w *world) iVoidTheNamedLabel(ref, reason string) error {
	v, err := usecases.NewVoidLabel(w.deps).Execute(context.Background(), label.Ref(ref), reason)
	w.lastLabel, w.lastErr = v, err
	return nil
}

func (w *world) iRecordAScanOn(typ, id string, hours int, tn string) error {
	_, err := usecases.NewRecordScan(w.deps).Execute(context.Background(), usecases.RecordScanCmd{
		TrackingNumber: shared.TrackingNumber(tn), ScanID: id, NodeID: "node-x", Type: journey.ScanType(typ), At: hour(hours),
	})
	w.lastErr = err
	return nil
}

// --- the collision that survives the retry ----------------------------------

func (w *world) nextTwoTrackingNumbersAreTaken() {
	taken := w.trackings[0]
	w.ids.Trackers = []shared.TrackingNumber{taken, taken}
}

// --- consumed arrival facts --------------------------------------------------

// linehaulArrives applies a LoadArrived fact. The node type decides the scan,
// exactly as the Kafka handler maps it: DELIVERY_STATION is a STATION_ARRIVAL,
// anything else a TRANSIT_ARRIVAL (ADR 0002 A20).
func (w *world) linehaulArrives(node, nodeType string, hours int) error {
	typ := journey.ScanTransitArrival
	if nodeType == "DELIVERY_STATION" {
		typ = journey.ScanStationArrival
	}
	_, err := usecases.NewApplyScans(w.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "linehaul-transportation", EventID: "ev-arr-1",
		Facts: []usecases.Fact{{TrackingNumber: w.tracking(), NodeID: node, Type: typ, At: hour(hours)}},
	})
	w.lastErr = err
	return nil
}

// lastMileDelivers applies a DeliveryAttempted fact with outcome DELIVERED and
// no node of its own: the scan takes the journey's last applied node (A21).
func (w *world) lastMileDelivers(hours int) error {
	_, err := usecases.NewApplyScans(w.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "last-mile-delivery", EventID: "ev-del-1",
		Facts: []usecases.Fact{{TrackingNumber: w.tracking(), Type: journey.ScanDelivered, At: hour(hours)}},
	})
	w.lastErr = err
	return nil
}
