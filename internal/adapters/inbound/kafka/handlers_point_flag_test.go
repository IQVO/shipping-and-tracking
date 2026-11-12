package kafka_test

import (
	"context"
	"testing"
	"time"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
)

const deliveryPointFlaggedType = "com.retailnetwork.logistics.last-mile-delivery.deliverypointprofile.DeliveryPointFlagged"

func TestDeliveryPointFlaggedIsDispatchedOnTheFullTypeAndMapped(t *testing.T) {
	if inbound.TypeDeliveryPointFlagged != deliveryPointFlaggedType {
		t.Fatalf("type = %q", inbound.TypeDeliveryPointFlagged)
	}
	flagger := &fakeFlagger{}
	c := consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, &fakeEtas{}, flagger, quiet), true)
	data := `{"stopKey":"k1","reason":"NOT_HOME","failures":2,"trackingNumbers":["RN1","RN2"],` +
		`"flaggedAt":"2026-10-23T16:30:00Z","extra":true}`
	if err := c.HandleMessage(context.Background(), envelope(t, "ev-flag", deliveryPointFlaggedType, data)); err != nil {
		t.Fatal(err)
	}
	if len(flagger.cmds) != 1 {
		t.Fatalf("cmds = %+v", flagger.cmds)
	}
	got := flagger.cmds[0]
	if got.Consumer != inbound.ConsumerLastMile || got.EventID != "ev-flag" || len(got.TrackingNumbers) != 2 ||
		got.TrackingNumbers[0] != "RN1" || got.TrackingNumbers[1] != "RN2" || got.Reason != "NOT_HOME" {
		t.Fatalf("cmd = %+v", got)
	}
	if !got.FlaggedAt.Equal(time.Date(2026, 10, 23, 16, 30, 0, 0, time.UTC)) {
		t.Fatalf("flaggedAt = %v", got.FlaggedAt)
	}
}

func TestDeliveryPointFlaggedBadPayloadsAreDeadLettered(t *testing.T) {
	bad := map[string]string{
		"no tracking numbers":    `{"reason":"NOT_HOME","flaggedAt":"2026-10-23T16:30:00Z"}`,
		"blank tracking number":  `{"trackingNumbers":["RN1",""],"reason":"NOT_HOME","flaggedAt":"2026-10-23T16:30:00Z"}`,
		"unknown reason":         `{"trackingNumbers":["RN1"],"reason":"SNOWSTORM","flaggedAt":"2026-10-23T16:30:00Z"}`,
		"no reason":              `{"trackingNumbers":["RN1"],"flaggedAt":"2026-10-23T16:30:00Z"}`,
		"tracking of wrong type": `{"trackingNumbers":"RN1","reason":"NOT_HOME","flaggedAt":"2026-10-23T16:30:00Z"}`,
	}
	for name, data := range bad {
		t.Run(name, func(t *testing.T) {
			c := consumerOf(inbound.LastMileHandlers(&fakeScans{}, &fakeReturner{}, &fakeEtas{}, &fakeFlagger{}, quiet), true)
			if err := c.HandleMessage(context.Background(), envelope(t, "ev-bad", deliveryPointFlaggedType, data)); err == nil {
				t.Fatalf("%s: a poison payload must surface to the loop so it is dead-lettered", name)
			}
		})
	}
}
