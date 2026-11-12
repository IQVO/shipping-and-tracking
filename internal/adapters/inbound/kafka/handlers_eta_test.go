package kafka_test

import (
	"context"
	"errors"
	"testing"
	"time"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
)

type fakeEtas struct {
	cmds []usecases.ApplyEtaWindowsCmd
	err  error
}

func (f *fakeEtas) Execute(_ context.Context, cmd usecases.ApplyEtaWindowsCmd) (usecases.EtaResult, error) {
	f.cmds = append(f.cmds, cmd)
	return usecases.EtaResult{Applied: len(cmd.TrackingNumbers)}, f.err
}

type fakeFlagger struct {
	cmds []usecases.RelayDeliveryPointFlagCmd
	err  error
}

func (f *fakeFlagger) Execute(_ context.Context, cmd usecases.RelayDeliveryPointFlagCmd) (usecases.PointFlagResult, error) {
	f.cmds = append(f.cmds, cmd)
	return usecases.PointFlagResult{Applied: len(cmd.TrackingNumbers)}, f.err
}

const stopEtaType = "com.retailnetwork.logistics.last-mile-delivery.route.StopEtaWindowAssigned"

func TestStopEtaWindowAssignedIsDispatchedOnTheFullTypeAndMapped(t *testing.T) {
	if inbound.TypeStopEtaWindowAssigned != stopEtaType {
		t.Fatalf("type = %q", inbound.TypeStopEtaWindowAssigned)
	}
	etas := &fakeEtas{}
	c := consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, etas, &fakeFlagger{}, quiet), true)
	data := `{"routeId":"rt-1","stopNo":2,"stopKey":"pkg:RN1","trackingNumbers":["RN1","RN2"],` +
		`"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z","extra":true}`
	if err := c.HandleMessage(context.Background(), envelope(t, "ev-eta", stopEtaType, data)); err != nil {
		t.Fatal(err)
	}
	if len(etas.cmds) != 1 {
		t.Fatalf("cmds = %+v", etas.cmds)
	}
	got := etas.cmds[0]
	if got.Consumer != inbound.ConsumerLastMile || got.EventID != "ev-eta" || len(got.TrackingNumbers) != 2 ||
		got.TrackingNumbers[0] != "RN1" || got.TrackingNumbers[1] != "RN2" {
		t.Fatalf("cmd = %+v", got)
	}
	if !got.Start.Equal(time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)) || !got.End.Equal(time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %v..%v", got.Start, got.End)
	}
	// The CloudEvents time of the test envelope is the assignedAt.
	if !got.AssignedAt.Equal(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("assignedAt = %v", got.AssignedAt)
	}
}

func TestStopEtaWindowAssignedBadPayloadsAreDeadLettered(t *testing.T) {
	bad := map[string]string{
		"no tracking numbers":    `{"trackingNumbers":[],"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z"}`,
		"absent tracking list":   `{"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z"}`,
		"blank tracking number":  `{"trackingNumbers":["RN1",""],"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z"}`,
		"start equals end":       `{"trackingNumbers":["RN1"],"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T13:00:00Z"}`,
		"start after end":        `{"trackingNumbers":["RN1"],"etaStart":"2026-10-23T15:00:00Z","etaEnd":"2026-10-23T13:00:00Z"}`,
		"no start":               `{"trackingNumbers":["RN1"],"etaEnd":"2026-10-23T13:00:00Z"}`,
		"no end":                 `{"trackingNumbers":["RN1"],"etaStart":"2026-10-23T13:00:00Z"}`,
		"tracking of wrong type": `{"trackingNumbers":"RN1","etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z"}`,
	}
	for name, data := range bad {
		t.Run(name, func(t *testing.T) {
			etas := &fakeEtas{}
			c := consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, etas, &fakeFlagger{}, quiet), true)
			if err := c.HandleMessage(context.Background(), envelope(t, "e", stopEtaType, data)); err == nil || len(etas.cmds) != 0 {
				t.Fatalf("err %v cmds %v, want a poison error and no command", err, etas.cmds)
			}
		})
	}
}

func TestStopEtaWindowAssignedWithoutAnyTimeIsPoisonAndATransientFailureIsReturned(t *testing.T) {
	etas := &fakeEtas{}
	c := consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, etas, &fakeFlagger{}, quiet), true)
	noTime := []byte(`{"specversion":"1.0","id":"e","source":"/t","type":"` + stopEtaType + `","datacontenttype":"application/json",` +
		`"data":{"trackingNumbers":["RN1"],"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z"}}`)
	if err := c.HandleMessage(context.Background(), noTime); err == nil || len(etas.cmds) != 0 {
		t.Fatalf("an event with no time has no assignedAt: err %v cmds %v", err, etas.cmds)
	}
	boom := errors.New("database down")
	c = consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, &fakeEtas{err: boom}, &fakeFlagger{}, quiet), true)
	data := `{"trackingNumbers":["RN1"],"etaStart":"2026-10-23T13:00:00Z","etaEnd":"2026-10-23T15:00:00Z"}`
	if err := c.HandleMessage(context.Background(), envelope(t, "e", stopEtaType, data)); !errors.Is(err, boom) {
		t.Fatalf("transient = %v", err)
	}
}
