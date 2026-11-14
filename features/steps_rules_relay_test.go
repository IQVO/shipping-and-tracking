package features_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// steps_rules_relay_test.go: the stop-level events of last-mile-delivery that
// are applied per tracking number and are NOT scans: the day-of ETA window
// (ADR 0013) and the delivery-point flag (ADR 0014).

// stopOf is every tracking number of the scenario, plus an unknown one on request.
func (r *rules) stopOf(withUnknown bool) []shared.TrackingNumber {
	tns := append([]shared.TrackingNumber(nil), r.w.trackings...)
	if withUnknown {
		tns = append(tns, unknownTracking)
	}
	return tns
}

func (r *rules) assignEta(from, to, assignedAt int, withUnknown bool) error {
	res, err := usecases.NewApplyEtaWindows(r.w.deps).Execute(context.Background(), usecases.ApplyEtaWindowsCmd{
		Consumer: "last-mile-delivery", EventID: fmt.Sprintf("ev-stop-eta-%d-%d-%d", from, to, assignedAt),
		TrackingNumbers: r.stopOf(withUnknown), Start: hour(from), End: hour(to), AssignedAt: hour(assignedAt),
	})
	r.relay = relayCounts{applied: res.Applied, stale: res.Stale, closed: res.Closed, unknown: res.Unknown, skipped: res.Skipped}
	r.w.lastErr = err
	return nil
}

func (r *rules) assignEtaWithoutStart(to, assignedAt int) error {
	_, r.w.lastErr = usecases.NewApplyEtaWindows(r.w.deps).Execute(context.Background(), usecases.ApplyEtaWindowsCmd{
		Consumer: "last-mile-delivery", EventID: "ev-eta-no-start", TrackingNumbers: r.stopOf(false), End: hour(to), AssignedAt: hour(assignedAt),
	})
	return nil
}

func (r *rules) flagPoint(reason string, flaggedAt int, withUnknown bool) error {
	res, err := usecases.NewRelayDeliveryPointFlag(r.w.deps).Execute(context.Background(), usecases.RelayDeliveryPointFlagCmd{
		Consumer: "last-mile-delivery", EventID: fmt.Sprintf("ev-flag-%s-%d", reason, flaggedAt),
		TrackingNumbers: r.stopOf(withUnknown), Reason: reason, FlaggedAt: hour(flaggedAt),
	})
	r.relay = relayCounts{applied: res.Applied, stale: res.Stale, closed: res.Closed, unknown: res.Unknown, skipped: res.Skipped}
	r.w.lastErr = err
	return nil
}

// nth maps "first" and "second" to a journey of the packages issued.
func (r *rules) nth(which string) (*journey.Journey, error) {
	i := map[string]int{"first": 0, "second": 1}[which]
	if i >= len(r.w.trackings) {
		return nil, fmt.Errorf("there is no %s package", which)
	}
	return r.w.store.Journeys().Get(context.Background(), r.w.trackings[i])
}

func (r *rules) registerRelay(sc *godog.ScenarioContext) {
	sc.Step(`^last-mile assigns an ETA window with no start that ends at hour (\d+) at hour (\d+)$`, r.assignEtaWithoutStart)
	sc.Step(`^last-mile assigns the ETA window hour (\d+) to hour (\d+) at hour (\d+) for every package$`, func(from, to, at int) error {
		return r.assignEta(from, to, at, false)
	})
	sc.Step(`^last-mile assigns the ETA window hour (\d+) to hour (\d+) at hour (\d+) for every package and an unknown package$`,
		func(from, to, at int) error { return r.assignEta(from, to, at, true) })
	sc.Step(`^last-mile flags the delivery point "([A-Z_]+)" at hour (\d+) for every package$`, func(reason string, at int) error {
		return r.flagPoint(reason, at, false)
	})
	sc.Step(`^last-mile flags the delivery point "([A-Z_]+)" at hour (\d+) for every package and an unknown package$`,
		func(reason string, at int) error { return r.flagPoint(reason, at, true) })
	sc.Step(`^the relayed event counted (\d+) applied, (\d+) stale, (\d+) closed and (\d+) unknown$`, r.relayedCounted)
	sc.Step(`^the relayed event was skipped as already processed$`, func() error {
		if !r.relay.skipped {
			return fmt.Errorf("relayed event counted %+v, want it skipped", r.relay)
		}
		return nil
	})
	sc.Step(`^the PackageEtaWindowAssigned events carry the orders "([^"]*)", assignment "([^"]*)" and the window hour (\d+) to hour (\d+) assigned at hour (\d+)$`, r.etaEvents)
	sc.Step(`^the delivery point of the (first|second) package is flagged "([A-Z_]+)" at hour (\d+)$`, r.pointFlagged)
	sc.Step(`^the (first|second) package has no delivery-point flag$`, r.noPointFlag)
	sc.Step(`^the PackageDeliveryPointFlagged events carry the reasons "([^"]*)" for the orders "([^"]*)" and assignment "([^"]*)"$`, r.flagEvents)
}

func (r *rules) relayedCounted(applied, stale, closed, unknown int) error {
	got := r.relay
	if got.skipped || got.applied != applied || got.stale != stale || got.closed != closed || got.unknown != unknown {
		return fmt.Errorf("relayed event counted %+v, want applied %d stale %d closed %d unknown %d", got, applied, stale, closed, unknown)
	}
	return nil
}

func (r *rules) etaEvents(orders, asg string, from, to, assignedAt int) error {
	var got []string
	for _, e := range allEvents[journey.PackageEtaWindowAssigned](r.events()) {
		if err := firstErr(diff("assignmentId", e.AssignmentID, asg), atHour("etaStart", e.EtaStart, from),
			atHour("etaEnd", e.EtaEnd, to), atHour("assignedAt", e.AssignedAt, assignedAt)); err != nil {
			return err
		}
		got = append(got, e.OrderID)
	}
	return diff("PackageEtaWindowAssigned orders", strings.Join(got, ","), orders)
}

func (r *rules) pointFlagged(which, reason string, flaggedAt int) error {
	j, err := r.nth(which)
	if err != nil {
		return err
	}
	return firstErr(diff("pointFlagReason of the "+which+" package", j.PointFlagReason(), reason), atHour("pointFlaggedAt", j.PointFlaggedAt(), flaggedAt))
}

func (r *rules) noPointFlag(which string) error {
	j, err := r.nth(which)
	if err != nil {
		return err
	}
	return firstErr(diff("pointFlagReason of the "+which+" package", j.PointFlagReason(), ""), diff("pointFlaggedAt zero", j.PointFlaggedAt().IsZero(), true))
}

func (r *rules) flagEvents(reasons, orders, asg string) error {
	var rs, os []string
	for _, e := range allEvents[journey.PackageDeliveryPointFlagged](r.events()) {
		if err := diff("assignmentId", e.AssignmentID, asg); err != nil {
			return err
		}
		rs, os = append(rs, e.Reason), append(os, e.OrderID)
	}
	return firstErr(diff("PackageDeliveryPointFlagged reasons", strings.Join(rs, ","), reasons), diff("PackageDeliveryPointFlagged orders", strings.Join(os, ","), orders))
}
