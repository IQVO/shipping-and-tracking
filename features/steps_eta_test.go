// Steps for eta_window.feature: the StopEtaWindowAssigned of last-mile-delivery
// goes through the ApplyEtaWindows use case the Kafka consumer calls.
package features_test

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

func registerEtaSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^last-mile assigns the ETA window hour (\d+) to hour (\d+) at hour (\d+)$`, w.lastMileAssignsEta)
	sc.Step(`^the timeline shows the ETA window hour (\d+) to hour (\d+)$`, w.timelineShowsEta)
	sc.Step(`^the timeline shows no ETA window$`, w.timelineShowsNoEta)
}

func (w *world) lastMileAssignsEta(from, to, assignedHour int) error {
	_, err := usecases.NewApplyEtaWindows(w.deps).Execute(context.Background(), usecases.ApplyEtaWindowsCmd{
		Consumer: "last-mile-delivery", EventID: fmt.Sprintf("ev-eta-%d-%d-%d", from, to, assignedHour),
		TrackingNumbers: []shared.TrackingNumber{w.tracking()}, Start: hour(from), End: hour(to), AssignedAt: hour(assignedHour),
	})
	w.lastErr = err
	return nil
}

func (w *world) timelineShowsEta(from, to int) error {
	v := w.lastJourney
	if v.EtaStart == nil || v.EtaEnd == nil {
		return fmt.Errorf("the timeline shows no ETA window, want hour %d to %d", from, to)
	}
	if !v.EtaStart.Equal(hour(from)) || !v.EtaEnd.Equal(hour(to)) {
		return fmt.Errorf("the timeline shows %s to %s, want hour %d to %d", v.EtaStart.Format(timeFormat), v.EtaEnd.Format(timeFormat), from, to)
	}
	return nil
}

func (w *world) timelineShowsNoEta() error {
	if v := w.lastJourney; v.EtaStart != nil || v.EtaEnd != nil {
		return fmt.Errorf("the timeline shows %v to %v, want no ETA window", v.EtaStart, v.EtaEnd)
	}
	return nil
}
