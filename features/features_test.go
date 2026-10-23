// Package features_test hosts the godog (Cucumber for Go) acceptance suite. Every
// scenario in this directory drives the REAL use cases over the in-memory
// doubles (the same repository contract the Postgres adapters pass), so each is
// a black-box test of the behaviour the model promises: the invariants of the
// label and the journey, the silence policy and the ship-to retention.
//
// The runner lives here, NOT at the repository root (TestNoGoFilesAtRepoRoot).
package features_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/outbound/memory"
	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/application/shipto"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// TestFeatures runs every Gherkin feature in this directory.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

var t0 = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

func hour(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }

// world is the per-scenario state.
type world struct {
	store   *memory.Store
	clock   *memory.Clock
	ids     *memory.SequentialIDs
	gateway *memory.ShipToGateway
	deps    usecases.Deps

	lastErr   error
	lastLabel views.Label
	trackings []shared.TrackingNumber // tracking numbers issued, in order
	labels    []string                // label refs issued, in order
	sweep     usecases.SweepResult
	loadEvent usecases.ApplyScansCmd
}

func newWorld() *world {
	s := memory.NewStore()
	w := &world{store: s, clock: memory.NewClock(t0), ids: &memory.SequentialIDs{}, gateway: memory.NewShipToGateway()}
	w.deps = usecases.Deps{
		Labels: s.Labels(), Snapshots: s.Snapshots(), Journeys: s.Journeys(), Catalog: s.Catalog(), Inbox: s.Inbox(),
		Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: w.clock, IDs: w.ids, ShipTo: w.gateway,
		Sealer: memory.MarkSealer{}, StopKeys: memory.HashKeyer{},
	}
	return w
}

func address(name string) shipto.Address {
	return shipto.Address{
		Name: name, Line1: "10 Sample Street", Line2: "Apt 4", City: "Sampletown", Region: "SP",
		PostalCode: "01310-100", CountryCode: "BR", Phone: "+55 11 5550 0100",
	}
}

// slug maps a use case error to the problem type the REST adapter answers.
func slug(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, label.ErrLabelAlreadyIssued):
		return "label-already-issued"
	case errors.Is(err, label.ErrTrackingNumberTaken):
		return "tracking-number-taken"
	case errors.Is(err, journey.ErrVoidAfterHandover):
		return "void-after-handover"
	case errors.Is(err, journey.ErrDuplicateScan):
		return "duplicate-scan"
	case errors.Is(err, journey.ErrScanOutOfOrder):
		return "scan-out-of-order"
	case errors.Is(err, journey.ErrJourneyClosed):
		return "journey-closed"
	case errors.Is(err, shared.ErrIllegalTransition):
		return "illegal-transition"
	case errors.Is(err, shared.ErrValidation):
		return "validation-failed"
	case errors.Is(err, ports.ErrShipToUnavailable):
		return "ship-to-unavailable"
	case errors.Is(err, ports.ErrNotFound):
		return "not-found"
	case errors.Is(err, ports.ErrConcurrentUpdate):
		return "concurrent-update"
	}
	return "unmapped: " + err.Error()
}

func (w *world) tracking() shared.TrackingNumber { return w.trackings[0] }

func (w *world) journey() (*journey.Journey, error) {
	return w.store.Journeys().Get(context.Background(), w.tracking())
}

// --- Given ---------------------------------------------------------------

func (w *world) orderShipsToSyntheticAddress(order string) {
	w.gateway.Put(order, address("Ana Example"))
}

func (w *world) orderShipsToSameAddressUnderAnotherName(order string) {
	w.gateway.Put(order, address("Bia Example"))
}

func (w *world) customerOrderingIsUnavailable() { w.gateway.Fail(true) }

func (w *world) labelHasBeenIssued(pkg int, po string) error {
	return w.issue(pkg, po, "ord-1", "NEXT_DAY")
}

func (w *world) issue(pkg int, po, order, tier string) error {
	v, err := usecases.NewIssueLabel(w.deps).Execute(context.Background(), usecases.IssueLabelCmd{
		PONumber: po, PackageNo: pkg, AssignmentID: "asg-1", OrderID: order, Tier: shared.Tier(tier),
	})
	w.lastErr, w.lastLabel = err, v
	if err == nil {
		w.trackings = append(w.trackings, shared.TrackingNumber(v.TrackingNumber))
		w.labels = append(w.labels, v.LabelRef)
	}
	return nil
}

func (w *world) mustIssue(pkg int, po string) error {
	if err := w.labelHasBeenIssued(pkg, po); err != nil {
		return err
	}
	return w.expectSuccess()
}

func (w *world) nextTrackingNumberIsTheTakenOne() {
	w.ids.Trackers = []shared.TrackingNumber{w.trackings[0]}
}

func (w *world) packageIsOutForDelivery() error {
	for i, typ := range []string{"PICKUP_DEPARTURE", "DEPARTURE", "STATION_ARRIVAL", "OUT_FOR_DELIVERY"} {
		if err := w.record(typ, fmt.Sprintf("g%d", i+1), i+1); err != nil {
			return err
		}
		if err := w.expectSuccess(); err != nil {
			return err
		}
	}
	return nil
}

// --- When ----------------------------------------------------------------

func (w *world) iIssueALabel(pkg int, po, order, tier string) error {
	return w.issue(pkg, po, order, tier)
}

func (w *world) iVoidTheLabel(reason string) error {
	v, err := usecases.NewVoidLabel(w.deps).Execute(context.Background(), label.Ref(w.labels[0]), reason)
	w.lastErr, w.lastLabel = err, v
	return nil
}

func (w *world) record(typ, id string, hours int) error {
	_, err := usecases.NewRecordScan(w.deps).Execute(context.Background(), usecases.RecordScanCmd{
		TrackingNumber: w.tracking(), ScanID: id, NodeID: "node-" + strings.ToLower(typ), Type: journey.ScanType(typ), At: hour(hours),
	})
	w.lastErr = err
	return nil
}

func (w *world) iRecordAScan(typ, id string, hours int) error { return w.record(typ, id, hours) }

func (w *world) lastMileAsksToReturn() error {
	_, err := usecases.NewReturnToSender(w.deps).Execute(context.Background(), usecases.ReturnCmd{
		Consumer: "lastmile", EventID: "ev-return", TrackingNumber: w.tracking(), ReturnedAt: hour(50),
	})
	w.lastErr = err
	return nil
}

func (w *world) clockMovesToHour(n int) { w.clock.Set(hour(n)) }

func (w *world) silenceSweepRuns() error {
	res, err := usecases.NewSweepSilence(w.deps).Execute(context.Background())
	w.sweep, w.lastErr = res, err
	return nil
}

func (w *world) secondLabelIssued(pkg int, po string) error { return w.mustIssue(pkg, po) }

func (w *world) oneLinehaulEventDepartsBoth(hours int, node string) error {
	var facts []usecases.Fact
	for _, tn := range w.trackings {
		facts = append(facts, usecases.Fact{TrackingNumber: tn, NodeID: node, Type: journey.ScanDeparture, At: hour(hours), PickupIfLabelled: true})
	}
	w.loadEvent = usecases.ApplyScansCmd{Consumer: "linehaul", EventID: "ev-load-1", Facts: facts}
	_, w.lastErr = usecases.NewApplyScans(w.deps).Execute(context.Background(), w.loadEvent)
	return w.expectSuccess()
}

func (w *world) sameLinehaulEventAgain() error {
	res, err := usecases.NewApplyScans(w.deps).Execute(context.Background(), w.loadEvent)
	if err != nil || !res.Skipped {
		return fmt.Errorf("a redelivered event must be skipped, got %+v %v", res, err)
	}
	return nil
}

func (w *world) departureWithoutKind(hours int) error {
	_, w.lastErr = usecases.NewApplyScans(w.deps).Execute(context.Background(), usecases.ApplyScansCmd{
		Consumer: "linehaul", EventID: "ev-no-kind",
		Facts: []usecases.Fact{{TrackingNumber: w.tracking(), NodeID: "fc-1", Type: journey.ScanDeparture, At: hour(hours), PickupIfLabelled: true}},
	})
	return w.expectSuccess()
}

func (w *world) clockMovesAfterClose(d time.Duration) error {
	j, err := w.journey()
	if err != nil {
		return err
	}
	w.clock.Set(j.ClosedAt().Add(d))
	return nil
}

func (w *world) shipToPurgeRuns() error {
	_, w.lastErr = usecases.NewPurgeShipTo(w.deps).Execute(context.Background())
	return w.expectSuccess()
}

// --- Then ----------------------------------------------------------------

func (w *world) expectSuccess() error {
	if w.lastErr != nil {
		return fmt.Errorf("want success, got %s (%v)", slug(w.lastErr), w.lastErr)
	}
	return nil
}

func (w *world) requestIsRefusedWith(want string) error {
	if got := slug(w.lastErr); got != want {
		return fmt.Errorf("want problem %q, got %q (%v)", want, got, w.lastErr)
	}
	return nil
}

func (w *world) labelHas(carrier, status string) error {
	if w.lastLabel.Carrier != carrier || w.lastLabel.Status != status {
		return fmt.Errorf("label carrier/status = %s/%s, want %s/%s", w.lastLabel.Carrier, w.lastLabel.Status, carrier, status)
	}
	return nil
}

func (w *world) journeyIs(state string) error {
	j, err := w.journey()
	if err != nil {
		return err
	}
	if string(j.State()) != state {
		return fmt.Errorf("journey is %s, want %s", j.State(), state)
	}
	return nil
}

func (w *world) journeyHasScans(total, applied int) error {
	j, err := w.journey()
	if err != nil {
		return err
	}
	got := 0
	for _, s := range j.Scans() {
		if s.Applied {
			got++
		}
	}
	if len(j.Scans()) != total || got != applied {
		return fmt.Errorf("scans = %d of which %d applied, want %d of which %d", len(j.Scans()), got, total, applied)
	}
	return nil
}

func (w *world) names() []string {
	var out []string
	for _, e := range w.store.Outbox().Events() {
		out = append(out, e.EventName())
	}
	return out
}

func (w *world) publishedEventsAre(want string) error {
	if got := strings.Join(w.names(), ","); got != want {
		return fmt.Errorf("published events = %s, want %s", got, want)
	}
	return nil
}

func (w *world) lastPublishedEventsAre(want string) error {
	n := len(strings.Split(want, ","))
	names := w.names()
	if len(names) < n {
		return fmt.Errorf("only %d events published, want the last %d to be %s", len(names), n, want)
	}
	if got := strings.Join(names[len(names)-n:], ","); got != want {
		return fmt.Errorf("last published events = %s, want %s", got, want)
	}
	return nil
}

func (w *world) count(name string) int {
	n := 0
	for _, got := range w.names() {
		if got == name {
			n++
		}
	}
	return n
}

func (w *world) publishedIncludeOnceEach(a, b string) error {
	if w.count(a) != 1 || w.count(b) != 1 {
		return fmt.Errorf("%s x%d, %s x%d, want one each", a, w.count(a), b, w.count(b))
	}
	return nil
}

func (w *world) publishedIncludeTimes(name string, n int) error {
	if w.count(name) != n {
		return fmt.Errorf("%s published %d times, want %d", name, w.count(name), n)
	}
	return nil
}

func (w *world) snapshotStored() error {
	if w.store.Counts().Snapshots != 1 {
		return fmt.Errorf("snapshots stored = %d, want 1", w.store.Counts().Snapshots)
	}
	return nil
}

func (w *world) noSnapshotStored() error {
	if w.store.Counts().Snapshots != 0 {
		return fmt.Errorf("snapshots stored = %d, want 0", w.store.Counts().Snapshots)
	}
	return nil
}

func (w *world) nothingStored() error {
	if c := w.store.Counts(); c.Labels+c.Journeys+c.Snapshots+c.OutboxEvents != 0 {
		return fmt.Errorf("counts = %+v, want nothing stored", c)
	}
	return nil
}

func (w *world) sameStopKey(prefix string) error {
	var keys, prefixes []string
	for _, e := range w.store.Outbox().Events() {
		if ev, ok := e.(label.LabelIssued); ok {
			keys, prefixes = append(keys, ev.StopKey), append(prefixes, ev.DestinationPostalPrefix)
		}
	}
	if len(keys) != 2 || keys[0] != keys[1] || prefixes[0] != prefix || prefixes[1] != prefix {
		return fmt.Errorf("stop keys %v prefixes %v, want one shared key and prefix %s", keys, prefixes, prefix)
	}
	return nil
}

func (w *world) noAddressAnywhere() error {
	haystack := fmt.Sprintf("%+v %#v", w.lastLabel, w.store.Outbox().Events())
	for _, part := range []string{"Sample Street", "Sampletown", "Ana Example", "01310-100", "5550", "Apt 4"} {
		if strings.Contains(haystack, part) {
			return fmt.Errorf("%q leaked into a published event or response", part)
		}
	}
	return nil
}

func (w *world) twoDifferentTrackingNumbers() error {
	if len(w.trackings) != 2 || w.trackings[0] == w.trackings[1] {
		return fmt.Errorf("tracking numbers = %v", w.trackings)
	}
	return nil
}

func (w *world) bothJourneysAre(state string) error {
	for _, tn := range w.trackings {
		j, err := w.store.Journeys().Get(context.Background(), tn)
		if err != nil {
			return err
		}
		if string(j.State()) != state {
			return fmt.Errorf("journey %s is %s, want %s", tn, j.State(), state)
		}
	}
	return nil
}

func (w *world) sweepResult(raised, lost int) error {
	if w.lastErr != nil {
		return w.lastErr
	}
	if w.sweep.Raised != raised || w.sweep.Lost != lost {
		return fmt.Errorf("sweep = %+v, want raised %d lost %d", w.sweep, raised, lost)
	}
	return nil
}

// InitializeScenario registers the steps.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = *newWorld()
		return ctx, nil
	})

	sc.Step(`^customer order "([^"]*)" ships to a synthetic address$`, func(o string) error { w.orderShipsToSyntheticAddress(o); return nil })
	sc.Step(`^customer order "([^"]*)" ships to the same address under another name$`, func(o string) error {
		w.orderShipsToSameAddressUnderAnotherName(o)
		return nil
	})
	sc.Step(`^customer-ordering is unavailable$`, func() error { w.customerOrderingIsUnavailable(); return nil })
	sc.Step(`^a label has been issued for package (\d+) of "([^"]*)"$`, w.mustIssue)
	sc.Step(`^a second label has been issued for package (\d+) of "([^"]*)"$`, w.secondLabelIssued)
	sc.Step(`^the next tracking number generated is the one already taken$`, func() error { w.nextTrackingNumberIsTheTakenOne(); return nil })
	sc.Step(`^the package is out for delivery$`, w.packageIsOutForDelivery)

	sc.Step(`^I issue a label for package (\d+) of "([^"]*)" for order "([^"]*)" on tier "([^"]*)"$`, w.iIssueALabel)
	sc.Step(`^I void the label with reason "([^"]*)"$`, w.iVoidTheLabel)
	sc.Step(`^I record a "([A-Z_]+)" scan "([^"]*)" at hour (\d+)$`, w.iRecordAScan)
	sc.Step(`^last-mile asks to return the package$`, w.lastMileAsksToReturn)
	sc.Step(`^the clock moves to hour (\d+)$`, func(n int) error { w.clockMovesToHour(n); return nil })
	sc.Step(`^the silence sweep runs$`, w.silenceSweepRuns)
	sc.Step(`^one linehaul event departs both packages at hour (\d+) from node "([^"]*)"$`, w.oneLinehaulEventDepartsBoth)
	sc.Step(`^the same linehaul event is delivered again$`, w.sameLinehaulEventAgain)
	sc.Step(`^a linehaul departure without a departure kind is applied at hour (\d+)$`, w.departureWithoutKind)
	sc.Step(`^the clock moves forward 30 days after the journey closed$`, func() error { return w.clockMovesAfterClose(30 * 24 * time.Hour) })
	sc.Step(`^the clock moves forward 30 days and a second after the journey closed$`, func() error {
		return w.clockMovesAfterClose(30*24*time.Hour + time.Second)
	})
	sc.Step(`^the ship-to purge runs$`, w.shipToPurgeRuns)

	sc.Step(`^the request succeeds$`, w.expectSuccess)
	sc.Step(`^the request is refused with "([a-z-]+)"$`, w.requestIsRefusedWith)
	sc.Step(`^the label has carrier "([^"]*)" and status "([^"]*)"$`, w.labelHas)
	sc.Step(`^the journey is "([A-Z_]+)"$`, w.journeyIs)
	sc.Step(`^the journey has (\d+) scans of which (\d+) applied$`, w.journeyHasScans)
	sc.Step(`^the published events are "([^"]*)"$`, w.publishedEventsAre)
	sc.Step(`^the last published events are "([^"]*)"$`, w.lastPublishedEventsAre)
	sc.Step(`^the published events include "([^"]*)" once and "([^"]*)" once$`, w.publishedIncludeOnceEach)
	sc.Step(`^the published events include "([^"]*)" (\d+) times$`, w.publishedIncludeTimes)
	sc.Step(`^a ship-to snapshot is stored$`, w.snapshotStored)
	sc.Step(`^no ship-to snapshot is stored$`, w.noSnapshotStored)
	sc.Step(`^nothing is stored$`, w.nothingStored)
	sc.Step(`^the two LabelIssued events carry the same stop key and postal prefix "([^"]*)"$`, w.sameStopKey)
	sc.Step(`^no published event or response contains any part of the address$`, w.noAddressAnywhere)
	sc.Step(`^the two labels have different tracking numbers$`, w.twoDifferentTrackingNumbers)
	sc.Step(`^both journeys are "([A-Z_]+)"$`, w.bothJourneysAre)
	sc.Step(`^the sweep raised (\d+) exceptions and declared (\d+) packages lost$`, w.sweepResult)
}
