package kafka_test

import (
	"testing"

	inbound "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/kafka"
)

// The composition-root constructors wire one consumer per producer topic
// (ADR 0003) without dialling the broker: kafkago constructs the reader and
// the DLQ writer lazily, so these tests assert the wiring (name, topic, the
// full CloudEvents types it dispatches, the dead-letter policy) and Close a
// consumer that never connected.
func TestConsumerConstructorsWireTopicHandlersAndDLQ(t *testing.T) {
	cases := []struct {
		name         string
		consumerName string
		topic        string
		build        func() *inbound.Consumer
		types        []string
	}{
		{
			name:         "vendor",
			consumerName: "vendor consumer",
			topic:        inbound.VendorTopic,
			build: func() *inbound.Consumer {
				return inbound.NewVendorConsumer([]string{"b1:9092"}, "vendor-group", &fakeScans{}, quiet)
			},
			types: []string{inbound.TypeVendorShipmentConfirmed},
		},
		{
			name:         "linehaul",
			consumerName: "linehaul consumer",
			topic:        inbound.LinehaulTopic,
			build: func() *inbound.Consumer {
				return inbound.NewLinehaulConsumer([]string{"b1:9092"}, "linehaul-group", &fakeScans{}, quiet)
			},
			types: []string{inbound.TypeLoadDeparted, inbound.TypeLoadArrived},
		},
		{
			name:         "last-mile",
			consumerName: "last-mile consumer",
			topic:        inbound.LastMileTopic,
			build: func() *inbound.Consumer {
				return inbound.NewLastMileConsumer([]string{"b1:9092"}, "lastmile-group", &fakeScans{}, &fakeReturner{}, quiet)
			},
			types: []string{inbound.TypePackageOutForDelivery, inbound.TypeDeliveryAttempted,
				inbound.TypePackageReturnInitiated, inbound.TypePackageReturnInitiatedOnRoute},
		},
		{
			name:         "catalog",
			consumerName: "catalog consumer",
			topic:        inbound.CatalogTopic,
			build: func() *inbound.Consumer {
				return inbound.NewCatalogConsumer([]string{"b1:9092"}, "catalog-group", &fakeCatalog{}, quiet)
			},
			types: []string{inbound.TypeCatalogItemCreated},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.build()
			defer func() {
				if err := c.Close(); err != nil {
					t.Fatalf("Close a never-dialled consumer: %v", err)
				}
			}()
			checkConsumerWiring(t, c, tc.consumerName, tc.topic, tc.types)
		})
	}
}

func checkConsumerWiring(t *testing.T, c *inbound.Consumer, name, topic string, types []string) {
	t.Helper()
	if c.Name != name {
		t.Errorf("Name = %q, want %q", c.Name, name)
	}
	if c.Topic != topic {
		t.Errorf("Topic = %q, want %q", c.Topic, topic)
	}
	if c.DLQ == nil {
		t.Error("DLQ writer is not wired")
	}
	if !c.DeadLetterInvalid {
		t.Error("DeadLetterInvalid must be on for a domain consumer")
	}
	if c.Reader == nil {
		t.Error("Reader is not wired")
	}
	if len(c.Handlers) != len(types) {
		t.Fatalf("Handlers = %v, want exactly %v", c.Handlers, types)
	}
	for _, typ := range types {
		if _, ok := c.Handlers[typ]; !ok {
			t.Errorf("no handler for the consumed type %q", typ)
		}
	}
}

// NewConsumerForTopic reuses the same wiring on an explicit topic, so an
// integration test can point the identical logic at a throwaway topic.
func TestNewConsumerForTopicWiresTheExplicitTopic(t *testing.T) {
	c := inbound.NewConsumerForTopic("probe", []string{"b1:9092"}, "topic.itest", "group", inbound.VendorHandlers(&fakeScans{}, quiet), quiet)
	defer c.Close()
	if c.Topic != "topic.itest" || c.Name != "probe" {
		t.Errorf("name/topic = %q/%q, want probe/topic.itest", c.Name, c.Topic)
	}
	if _, ok := c.Handlers[inbound.TypeVendorShipmentConfirmed]; !ok {
		t.Error("the vendor handler map was not wired")
	}
	if c.DLQ == nil || !c.DeadLetterInvalid {
		t.Error("the dead-letter policy of a domain consumer is missing")
	}
}

// A quiet logger must be acceptable (defaultLogger keeps slog.Default for
// nil, which would spam test output).
func TestConstructorsAcceptADiscardLogger(t *testing.T) {
	c := inbound.NewVendorConsumer([]string{"b1:9092"}, "g", &fakeScans{}, quiet)
	defer c.Close()
	if c.Logger != quiet {
		t.Error("the supplied logger was not kept")
	}
}
