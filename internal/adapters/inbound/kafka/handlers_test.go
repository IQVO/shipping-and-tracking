package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/application/repository"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// envelope renders a structured-mode CloudEvents 1.0 JSON message.
func envelope(t *testing.T, id, typ, data string) []byte {
	t.Helper()
	e := ce.New()
	e.SetID(id)
	e.SetType(typ)
	e.SetSource("/test")
	e.SetTime(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	if err := e.SetData("application/json", []byte(data)); err != nil {
		t.Fatal(err)
	}
	b, err := e.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fakeScans struct {
	cmds []usecases.ApplyScansCmd
	err  error
}

func (f *fakeScans) Execute(_ context.Context, cmd usecases.ApplyScansCmd) (usecases.ApplyResult, error) {
	f.cmds = append(f.cmds, cmd)
	return usecases.ApplyResult{Applied: len(cmd.Facts)}, f.err
}

type fakeReturner struct {
	cmds []usecases.ReturnCmd
	err  error
}

func (f *fakeReturner) Execute(_ context.Context, cmd usecases.ReturnCmd) (usecases.ReturnResult, error) {
	f.cmds = append(f.cmds, cmd)
	return usecases.ReturnResult{Returned: f.err == nil}, f.err
}

type fakeCatalog struct {
	got []repository.CatalogDimensions
	err error
}

func (f *fakeCatalog) Execute(_ context.Context, _, _ string, d repository.CatalogDimensions) (bool, error) {
	f.got = append(f.got, d)
	return true, f.err
}

func consumerOf(handlers map[string]inbound.HandlerFunc, dlq bool) *inbound.Consumer {
	c := &inbound.Consumer{Name: "test", Topic: "t", Handlers: handlers, Logger: quiet}
	if dlq {
		c.DLQ = &fakeDLQ{}
		c.DeadLetterInvalid = true
	}
	return c
}

type fakeDLQ struct{ msgs []kafkago.Message }

func (f *fakeDLQ) WriteMessages(_ context.Context, m ...kafkago.Message) error {
	f.msgs = append(f.msgs, m...)
	return nil
}
func (f *fakeDLQ) Close() error { return nil }

var shippedAt = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)

func TestEveryConsumedTypeBecomesTheDocumentedFacts(t *testing.T) {
	cases := []struct {
		name, typ, data string
		wantType        journey.ScanType
		wantNode        string
		wantPickup      bool
		wantTracking    []string
		consumer        string
		wantAt          time.Time
	}{
		{"vendor", inbound.TypeVendorShipmentConfirmed,
			`{"poNumber":"PO-1","nodeId":"v-1","trackingNumbers":["RN1","RN2"],"shippedAt":"2026-10-23T09:00:00Z","extra":1}`,
			journey.ScanShipmentConfirmed, "v-1", false, []string{"RN1", "RN2"}, inbound.ConsumerVendor, shippedAt},
		{"departed pickup", inbound.TypeLoadDeparted,
			`{"originNodeId":"fc-1","trackingNumbers":["RN1"],"departedAt":"2026-10-23T09:00:00Z","departureKind":"PICKUP"}`,
			journey.ScanPickupDeparture, "fc-1", false, []string{"RN1"}, inbound.ConsumerLinehaul, shippedAt},
		{"departed other kind", inbound.TypeLoadDeparted,
			`{"originNodeId":"fc-1","trackingNumbers":["RN1"],"departedAt":"2026-10-23T09:00:00Z","departureKind":"LINEHAUL"}`,
			journey.ScanDeparture, "fc-1", false, []string{"RN1"}, inbound.ConsumerLinehaul, shippedAt},
		{"departed without kind", inbound.TypeLoadDeparted,
			`{"originNodeId":"fc-1","trackingNumbers":["RN1"],"departedAt":"2026-10-23T09:00:00Z"}`,
			journey.ScanDeparture, "fc-1", true, []string{"RN1"}, inbound.ConsumerLinehaul, shippedAt},
		{"arrived at a station", inbound.TypeLoadArrived,
			`{"destinationNodeId":"ds-1","trackingNumbers":["RN1"],"arrivedAt":"2026-10-23T09:00:00Z","nodeType":"DELIVERY_STATION"}`,
			journey.ScanStationArrival, "ds-1", false, []string{"RN1"}, inbound.ConsumerLinehaul, shippedAt},
		{"arrived at a station by fallback type", inbound.TypeLoadArrived,
			`{"destinationNodeId":"ds-1","trackingNumbers":["RN1"],"arrivedAt":"2026-10-23T09:00:00Z","destinationNodeType":"DELIVERY_STATION"}`,
			journey.ScanStationArrival, "ds-1", false, []string{"RN1"}, inbound.ConsumerLinehaul, shippedAt},
		{"arrived at a hub", inbound.TypeLoadArrived,
			`{"destinationNodeId":"hub-1","trackingNumbers":["RN1"],"arrivedAt":"2026-10-23T09:00:00Z","nodeType":"HUB"}`,
			journey.ScanTransitArrival, "hub-1", false, []string{"RN1"}, inbound.ConsumerLinehaul, shippedAt},
		{"arrived without type, time from the envelope", inbound.TypeLoadArrived,
			`{"destinationNodeId":"hub-1","trackingNumbers":["RN1"]}`,
			journey.ScanTransitArrival, "hub-1", false, []string{"RN1"}, inbound.ConsumerLinehaul, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)},
		{"out for delivery", inbound.TypePackageOutForDelivery,
			`{"trackingNumber":"RN1","stationNodeId":"ds-1","at":"2026-10-23T09:00:00Z"}`,
			journey.ScanOutForDelivery, "ds-1", false, []string{"RN1"}, inbound.ConsumerLastMile, shippedAt},
		{"delivered", inbound.TypeDeliveryAttempted,
			`{"trackingNumber":"RN1","outcome":"DELIVERED","at":"2026-10-23T09:00:00Z"}`,
			journey.ScanDelivered, "", false, []string{"RN1"}, inbound.ConsumerLastMile, shippedAt},
		{"failed attempt", inbound.TypeDeliveryAttempted,
			`{"trackingNumber":"RN1","outcome":"NOBODY_HOME","at":"2026-10-23T09:00:00Z"}`,
			journey.ScanDeliveryFailed, "", false, []string{"RN1"}, inbound.ConsumerLastMile, shippedAt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkFacts(t, c.typ, c.data, c.consumer, c.wantTracking, usecases.Fact{
				NodeID: c.wantNode, Type: c.wantType, At: c.wantAt, PickupIfLabelled: c.wantPickup,
			})
		})
	}
}

// allScanHandlers merges the handlers of the three scan-producing consumers.
func allScanHandlers(scans *fakeScans) map[string]inbound.HandlerFunc {
	handlers := map[string]inbound.HandlerFunc{}
	for _, m := range []map[string]inbound.HandlerFunc{
		inbound.VendorHandlers(scans, quiet), inbound.LinehaulHandlers(scans, quiet), inbound.LastMileHandlers(scans, &fakeReturner{}, quiet),
	} {
		for k, v := range m {
			handlers[k] = v
		}
	}
	return handlers
}

// checkFacts feeds one event and checks the single command it produced: one
// fact per tracking number, each equal to want but for its tracking number.
func checkFacts(t *testing.T, typ, data, consumer string, tracking []string, want usecases.Fact) {
	t.Helper()
	scans := &fakeScans{}
	if err := consumerOf(allScanHandlers(scans), true).HandleMessage(context.Background(), envelope(t, "ev-1", typ, data)); err != nil {
		t.Fatal(err)
	}
	if len(scans.cmds) != 1 || scans.cmds[0].Consumer != consumer || scans.cmds[0].EventID != "ev-1" || len(scans.cmds[0].Facts) != len(tracking) {
		t.Fatalf("cmds = %+v", scans.cmds)
	}
	for i, f := range scans.cmds[0].Facts {
		want.TrackingNumber = shared.TrackingNumber(tracking[i])
		if f != want {
			t.Errorf("fact %d = %+v, want %+v", i, f, want)
		}
	}
}

func TestReturnInitiatedOnEitherEntitySegment(t *testing.T) {
	for _, typ := range []string{inbound.TypePackageReturnInitiated, inbound.TypePackageReturnInitiatedOnRoute} {
		r := &fakeReturner{}
		c := consumerOf(inbound.LastMileHandlers(&fakeScans{}, r, quiet), true)
		if err := c.HandleMessage(context.Background(), envelope(t, "ev-r", typ, `{"trackingNumber":"RN1","at":"2026-10-23T09:00:00Z"}`)); err != nil {
			t.Fatal(err)
		}
		want := usecases.ReturnCmd{Consumer: inbound.ConsumerLastMile, EventID: "ev-r", TrackingNumber: "RN1", ReturnedAt: shippedAt}
		if len(r.cmds) != 1 || r.cmds[0] != want {
			t.Fatalf("%s: cmds = %+v", typ, r.cmds)
		}
	}
}

func TestCatalogItemCreatedIsMapped(t *testing.T) {
	cat := &fakeCatalog{}
	c := consumerOf(inbound.CatalogHandlers(cat, quiet), true)
	data := `{"catalogItemId":"item-1","title":"Lamp","lengthMm":300,"widthMm":200,"heightMm":100,"weightG":900,"hazmat":true,"fragile":true,"price":5}`
	if err := c.HandleMessage(context.Background(), envelope(t, "ev-c", inbound.TypeCatalogItemCreated, data)); err != nil {
		t.Fatal(err)
	}
	want := repository.CatalogDimensions{CatalogItemID: "item-1", Title: "Lamp", LengthMm: 300, WidthMm: 200, HeightMm: 100, WeightG: 900, Hazmat: true, Fragile: true}
	if len(cat.got) != 1 || cat.got[0] != want {
		t.Fatalf("got %+v", cat.got)
	}
}

func TestMessagesThatAreNotForThisConsumerAreAcknowledged(t *testing.T) {
	scans := &fakeScans{}
	c := consumerOf(inbound.LinehaulHandlers(scans, quiet), true)
	for name, value := range map[string][]byte{
		"not json":       []byte(`not json`),
		"not cloudevent": []byte(`{"hello":"world"}`),
		"unknown type":   envelope(t, "e", "com.retailnetwork.logistics.linehaul-transportation.load.LoadPlanned", `{}`),
		"no packages":    envelope(t, "e", inbound.TypeLoadDeparted, `{"originNodeId":"fc-1","trackingNumbers":[],"departedAt":"2026-10-23T09:00:00Z"}`),
	} {
		if err := c.HandleMessage(context.Background(), value); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(scans.cmds) != 0 {
		t.Fatalf("nothing should be applied, got %+v", scans.cmds)
	}
}

func TestPoisonMessagesAreDeadLetteredAndTransientOnesAreRetried(t *testing.T) {
	bad := []struct{ name, typ, data string }{
		{"payload is not an object", inbound.TypeLoadArrived, `"x"`},
		{"tracking numbers of the wrong type", inbound.TypeLoadArrived, `{"trackingNumbers":"RN1"}`},
		{"no time anywhere", inbound.TypeLoadArrived, ""},
	}
	for _, b := range bad {
		scans := &fakeScans{}
		c := consumerOf(inbound.LinehaulHandlers(scans, quiet), true)
		value := envelope(t, "e", b.typ, b.data)
		if b.data == "" {
			value = []byte(`{"specversion":"1.0","id":"e","source":"/t","type":"` + b.typ + `","datacontenttype":"application/json","data":{"trackingNumbers":["RN1"]}}`)
		}
		err := c.HandleMessage(context.Background(), value)
		if err == nil || len(scans.cmds) != 0 {
			t.Errorf("%s: err %v cmds %v, want a poison error and no command", b.name, err, scans.cmds)
		}
	}

	// A validation error from the use case is poison too; with no DLQ it is skipped.
	scans := &fakeScans{err: fmt.Errorf("%w: bad", shared.ErrValidation)}
	data := `{"destinationNodeId":"h","trackingNumbers":["RN1"],"arrivedAt":"2026-10-23T09:00:00Z"}`
	if err := consumerOf(inbound.LinehaulHandlers(scans, quiet), true).HandleMessage(context.Background(), envelope(t, "e", inbound.TypeLoadArrived, data)); err == nil {
		t.Error("a validation failure must be dead-lettered")
	}
	if err := consumerOf(inbound.LinehaulHandlers(scans, quiet), false).HandleMessage(context.Background(), envelope(t, "e", inbound.TypeLoadArrived, data)); err != nil {
		t.Errorf("without a DLQ a poison event is skipped, got %v", err)
	}

	// An infrastructure failure and an early return are transient: returned as they are.
	boom := errors.New("database down")
	if err := consumerOf(inbound.LinehaulHandlers(&fakeScans{err: boom}, quiet), true).HandleMessage(context.Background(), envelope(t, "e", inbound.TypeLoadArrived, data)); !errors.Is(err, boom) {
		t.Errorf("transient = %v", err)
	}
	notYet := fmt.Errorf("%w: not at the station", shared.ErrIllegalTransition)
	r := &fakeReturner{err: notYet}
	err := consumerOf(inbound.LastMileHandlers(&fakeScans{}, r, quiet), true).HandleMessage(context.Background(),
		envelope(t, "e", inbound.TypePackageReturnInitiated, `{"trackingNumber":"RN1"}`))
	if !errors.Is(err, notYet) {
		t.Errorf("a return before the station is retried, got %v", err)
	}
	if err := consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, quiet), true).HandleMessage(context.Background(),
		envelope(t, "e", inbound.TypePackageReturnInitiated, `{"at":"2026-10-23T09:00:00Z"}`)); err == nil {
		t.Error("a return without a tracking number is poison")
	}
}
