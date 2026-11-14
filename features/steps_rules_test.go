package features_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// steps_rules_test.go: vocabulary of the documentation-derived *_rules.feature
// files. It drives a package to a state, delivers consumed facts under a chosen
// CloudEvents id, reads the published events field by field and keeps the
// counters a consumer would log. Everything goes through the use cases the REST
// adapter and the Kafka consumers call, over the in-memory doubles.

// rules is the extra per-scenario state of the *_rules.feature files.
type rules struct {
	w        *world
	apply    usecases.ApplyResult
	relay    relayCounts
	baseline int // events published when the package reached its state
}

// relayCounts is what a consumed ETA or delivery-point event did.
type relayCounts struct {
	applied, stale, closed, unknown int
	skipped                         bool
}

// registerRuleSteps registers every step of the *_rules.feature files.
func registerRuleSteps(sc *godog.ScenarioContext, w *world) {
	r := &rules{w: w}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*r = rules{w: w}
		return ctx, nil
	})
	r.registerDrive(sc)
	r.registerEvents(sc)
	r.registerConsumed(sc)
	r.registerLabels(sc)
	r.registerRelay(sc)
}

// --- driving a package to a state -----------------------------------------

// statePath is the shortest run of scans from LABELLED to each open state.
var statePath = map[string][]journey.ScanType{
	"LABELLED":         nil,
	"HANDED_OVER":      {journey.ScanPickupDeparture},
	"IN_TRANSIT":       {journey.ScanPickupDeparture, journey.ScanDeparture},
	"AT_STATION":       {journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival},
	"OUT_FOR_DELIVERY": {journey.ScanPickupDeparture, journey.ScanDeparture, journey.ScanStationArrival, journey.ScanOutForDelivery},
}

// drive walks one journey to the state; the scan k of the path is p<k> at hour k.
func (r *rules) drive(tn shared.TrackingNumber, state string) error {
	path, ok := statePath[state]
	if !ok {
		return fmt.Errorf("no scan path to state %q", state)
	}
	for i, typ := range path {
		_, err := usecases.NewRecordScan(r.w.deps).Execute(context.Background(), usecases.RecordScanCmd{
			TrackingNumber: tn, ScanID: fmt.Sprintf("p%d", i+1), NodeID: "node-" + strings.ToLower(string(typ)),
			Type: typ, At: hour(i + 1),
		})
		if err != nil {
			return fmt.Errorf("driving %s to %s: %w", tn, state, err)
		}
	}
	r.baseline = len(r.w.names())
	return nil
}

func (r *rules) packageReached(state string) error { return r.drive(r.w.tracking(), state) }

func (r *rules) everyPackageReached(state string) error {
	for _, tn := range r.w.trackings {
		if err := r.drive(tn, state); err != nil {
			return err
		}
	}
	return nil
}

func (r *rules) tierPackageReached(tier, state string) error {
	if err := r.w.issue(1, "PO-1", "ord-1", tier); err != nil {
		return err
	}
	if err := r.w.expectSuccess(); err != nil {
		return err
	}
	return r.drive(r.w.tracking(), state)
}

// ended brings the package to one of the four end states by the documented way.
func (r *rules) ended(end string) error {
	switch end {
	case "VOIDED":
		return r.expect(r.w.iVoidTheLabel("order cancelled"))
	case "DELIVERED", "RETURNED":
		if err := r.drive(r.w.tracking(), "OUT_FOR_DELIVERY"); err != nil {
			return err
		}
		if end == "DELIVERED" {
			return r.expect(r.w.record("DELIVERED", "p5", 5))
		}
		if err := r.expect(r.w.record("DELIVERY_FAILED", "p5", 5)); err != nil {
			return err
		}
		return r.expect(r.w.lastMileAsksToReturn())
	case "LOST":
		if err := r.drive(r.w.tracking(), "IN_TRANSIT"); err != nil {
			return err
		}
		r.w.clockMovesToHour(26)
		if err := r.expect(r.w.silenceSweepRuns()); err != nil {
			return err
		}
		r.w.clockMovesToHour(74)
		return r.expect(r.w.silenceSweepRuns())
	}
	return fmt.Errorf("unknown end state %q", end)
}

// expect turns "the step ran" plus the world's last error into one result.
func (r *rules) expect(err error) error {
	if err != nil {
		return err
	}
	return r.w.expectSuccess()
}

func (r *rules) registerDrive(sc *godog.ScenarioContext) {
	sc.Step(`^the package has reached "([A-Z_]+)"$`, r.packageReached)
	sc.Step(`^every package has reached "([A-Z_]+)"$`, r.everyPackageReached)
	sc.Step(`^a "([A-Z_]+)" package has reached "([A-Z_]+)"$`, r.tierPackageReached)
	sc.Step(`^a package that ended as "([A-Z_]+)"$`, r.ended)
	sc.Step(`^the clock moves to hour (\d+) plus (\d+) minutes$`, func(h, m int) error {
		r.w.clock.Set(hour(h).Add(time.Duration(m) * time.Minute))
		return nil
	})
	sc.Step(`^the silence sweep runs reading (\d+) journeys per page$`, func(n int) error {
		sweep := usecases.NewSweepSilence(r.w.deps)
		sweep.PageSize = n
		res, err := sweep.Execute(context.Background())
		r.w.sweep, r.w.lastErr = res, err
		return nil
	})
	sc.Step(`^no event was published since the package reached its state$`, func() error {
		if got := len(r.w.names()); got != r.baseline {
			return fmt.Errorf("%d events published since the state was reached, want none: %s", got-r.baseline, strings.Join(r.w.names()[r.baseline:], ","))
		}
		return nil
	})
	sc.Step(`^the last scan is stored as not applied$`, func() error {
		j, err := r.w.journey()
		if err != nil {
			return err
		}
		scans := j.Scans()
		if len(scans) == 0 || scans[len(scans)-1].Applied {
			return fmt.Errorf("the last stored scan is applied or missing, want stored as not applied")
		}
		return nil
	})
	sc.Step(`^I record a "([A-Z_]+)" scan "([^"]*)" at node "([^"]*)" at hour (\d+)$`, func(typ, id, node string, h int) error {
		_, err := usecases.NewRecordScan(r.w.deps).Execute(context.Background(), usecases.RecordScanCmd{
			TrackingNumber: r.w.tracking(), ScanID: id, NodeID: node, Type: journey.ScanType(typ), At: hour(h),
		})
		r.w.lastErr = err
		return nil
	})
	sc.Step(`^the timeline carries no handover or delivery instant$`, func() error {
		if v := r.w.lastJourney; v.HandedOverAt != nil || v.DeliveredAt != nil {
			return fmt.Errorf("handedOverAt %v deliveredAt %v, want neither", v.HandedOverAt, v.DeliveredAt)
		}
		return nil
	})
	sc.Step(`^the timeline names purchase order "([^"]*)", order "([^"]*)", assignment "([^"]*)" and tier "([^"]*)"$`,
		func(po, order, asg, tier string) error {
			v := r.w.lastJourney
			if v.PONumber != po || v.OrderID != order || v.AssignmentID != asg || v.Tier != tier {
				return fmt.Errorf("timeline names %s/%s/%s/%s, want %s/%s/%s/%s", v.PONumber, v.OrderID, v.AssignmentID, v.Tier, po, order, asg, tier)
			}
			return nil
		})
}

// --- published events, field by field ---------------------------------------

func lastEvent[T any](evs []shared.Event) (T, error) {
	var zero T
	for i := len(evs) - 1; i >= 0; i-- {
		if e, ok := evs[i].(T); ok {
			return e, nil
		}
	}
	return zero, fmt.Errorf("no %T was published", zero)
}

func allEvents[T any](evs []shared.Event) []T {
	var out []T
	for _, e := range evs {
		if t, ok := e.(T); ok {
			out = append(out, t)
		}
	}
	return out
}

func (r *rules) events() []shared.Event { return r.w.store.Outbox().Events() }

// --- consumed facts under a chosen CloudEvents id ----------------------------

// unknownTracking is a tracking number this service never issued.
const unknownTracking = shared.TrackingNumber("RN999999999999999")

// consume delivers one consumed event under the given CloudEvents id: one fact
// per tracking number, exactly as the Kafka handlers build them.
func (r *rules) consume(eventID, typ string, h int, tns []shared.TrackingNumber, pickupIfLabelled bool) error {
	var facts []usecases.Fact
	for _, tn := range tns {
		facts = append(facts, usecases.Fact{TrackingNumber: tn, NodeID: "node-x", Type: journey.ScanType(typ), At: hour(h), PickupIfLabelled: pickupIfLabelled})
	}
	res, err := usecases.NewApplyScans(r.w.deps).Execute(context.Background(), usecases.ApplyScansCmd{Consumer: "test-consumer", EventID: eventID, Facts: facts})
	r.apply, r.w.lastErr = res, err
	return nil
}

func (r *rules) departure(kind string, node string, h int) error {
	typ, promote := journey.ScanDeparture, kind == ""
	if kind == "PICKUP" {
		typ = journey.ScanPickupDeparture
	}
	res, err := usecases.NewApplyScans(r.w.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "linehaul-transportation", EventID: "ev-dep-" + kind,
		Facts: []usecases.Fact{{TrackingNumber: r.w.tracking(), NodeID: node, Type: typ, At: hour(h), PickupIfLabelled: promote}},
	})
	r.apply, r.w.lastErr = res, err
	return nil
}

func (r *rules) registerConsumed(sc *godog.ScenarioContext) {
	sc.Step(`^a consumed event "([^"]*)" carries a "([A-Z_]+)" fact at hour (\d+) for every package$`, func(id, typ string, h int) error {
		return r.consume(id, typ, h, r.w.trackings, false)
	})
	sc.Step(`^a consumed event "([^"]*)" carries a "([A-Z_]+)" fact at hour (\d+) for every package and an unknown package$`,
		func(id, typ string, h int) error {
			return r.consume(id, typ, h, append(append([]shared.TrackingNumber(nil), r.w.trackings...), unknownTracking), false)
		})
	sc.Step(`^a linehaul departure of kind "([A-Z]*)" from node "([^"]*)" is applied at hour (\d+)$`, r.departure)
	sc.Step(`^the consumed event was applied to (\d+), stored for (\d+), refused as closed for (\d+) and unknown for (\d+) packages$`,
		func(applied, stored, closed, unknown int) error {
			a := r.apply
			if a.Skipped || a.Applied != applied || a.Stored != stored || a.Closed != closed || a.Unknown != unknown {
				return fmt.Errorf("consumed event counted %+v, want applied %d stored %d closed %d unknown %d", a, applied, stored, closed, unknown)
			}
			return nil
		})
	sc.Step(`^the consumed event was skipped as already processed$`, func() error {
		if !r.apply.Skipped {
			return fmt.Errorf("consumed event counted %+v, want it skipped", r.apply)
		}
		return nil
	})
	sc.Step(`^the last scan of the timeline has the id of event "([^"]*)" and the tracking number$`, func(ev string) error {
		scans := r.w.lastJourney.Scans
		if len(scans) == 0 {
			return fmt.Errorf("the timeline has no scans")
		}
		if got, want := scans[len(scans)-1].ScanID, ev+":"+string(r.w.tracking()); got != want {
			return fmt.Errorf("last scan id = %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^last-mile asks to return the unknown package$`, func() error {
		_, r.w.lastErr = usecases.NewReturnToSender(r.w.deps).Execute(context.Background(), usecases.ReturnCmd{
			Consumer: "lastmile", EventID: "ev-return-unknown", TrackingNumber: unknownTracking, ReturnedAt: hour(50),
		})
		return nil
	})
}
