package features_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	outkafka "github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
)

// steps_station_test.go: the vocabulary of destination_station.feature (ADR 0019).

func registerStationSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the area "([^"]*)" is assigned to delivery station "([^"]*)" at version (\d+)$`, w.areaAssigned)
	sc.Step(`^the area "([^"]*)" was assigned at version (\d+) without a delivery station$`, func(prefix string, v int) error {
		return w.areaAssigned(prefix, "", v)
	})
	sc.Step(`^the assignment of area "([^"]*)" is replayed at version (\d+) with delivery station "([^"]*)"$`, func(prefix string, v int, station string) error {
		return w.areaAssigned(prefix, station, v)
	})
	sc.Step(`^the area "([^"]*)" holds delivery station "([^"]*)" at version (\d+)$`, w.areaHolds)
	sc.Step(`^the published LabelIssued carries destination station "([^"]*)"$`, w.issuedCarriesStation)
	sc.Step(`^the published LabelIssued carries no destination station$`, func() error { return w.issuedCarriesStation("") })
	sc.Step(`^the wire payload of the published LabelIssued has no destinationStationNodeId$`, w.wireOmitsStation)
}

// areaAssigned folds one ServiceAreaAssigned through the real use case under a
// fresh CloudEvents id each time: a re-emit or a replay of the assignment.
func (w *world) areaAssigned(prefix, station string, version int) error {
	w.areaEvents++
	_, err := usecases.NewApplyAreaAssignment(w.deps).Execute(context.Background(), usecases.ApplyAreaCmd{
		Consumer: "node-topology.area-modes", EventID: fmt.Sprintf("area-ev-%d", w.areaEvents),
		Area: repository.AreaMode{PostalPrefix: prefix, DeliveryMode: repository.DeliveryModeNetworkRoute, Version: int64(version),
			DeliveryStationNodeID: station},
	})
	return err
}

func (w *world) areaHolds(prefix, station string, version int) error {
	a, err := w.store.Areas().Get(context.Background(), prefix)
	if err != nil {
		return err
	}
	if a.DeliveryStationNodeID != station || a.Version != int64(version) {
		return fmt.Errorf("area %s holds station %q at version %d, want %q at %d", prefix, a.DeliveryStationNodeID, a.Version, station, version)
	}
	return nil
}

func (w *world) issuedEvent() (label.LabelIssued, error) {
	for _, e := range w.store.Outbox().Events() {
		if ev, ok := e.(label.LabelIssued); ok {
			return ev, nil
		}
	}
	return label.LabelIssued{}, fmt.Errorf("no LabelIssued was published")
}

func (w *world) issuedCarriesStation(want string) error {
	ev, err := w.issuedEvent()
	if err != nil {
		return err
	}
	if ev.DestinationStationNodeID != want {
		return fmt.Errorf("LabelIssued destination station = %q, want %q", ev.DestinationStationNodeID, want)
	}
	return nil
}

func (w *world) wireOmitsStation() error {
	ev, err := w.issuedEvent()
	if err != nil {
		return err
	}
	msgs, err := outkafka.NewEncoder().Encode(ev)
	if err != nil {
		return err
	}
	if strings.Contains(string(msgs[0].Value), "destinationStationNodeId") {
		return fmt.Errorf("the wire payload carries the field: %s", msgs[0].Value)
	}
	return nil
}
