// Package kafka is the outbound Kafka adapter: it encodes the domain events of
// the label and journey aggregates into CloudEvents 1.0 outbox messages
// (Encoder) and writes drained outbox rows to the broker (RelaySink). Envelopes
// are built ONLY through internal/adapters/kafka/cloudevents. Payloads are
// exactly the ones pinned in apis/asyncapi.yaml.
package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/shipping-and-tracking/internal/application/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Topic is this service's integration-event topic. Every published event is an
// integration event (ADR 0004), so nothing goes to the analytics topic.
const Topic = "retailnetwork.shipping-and-tracking.events"

// schemaVersion is the dataschema version of every payload below
// (urn:retailnetwork:shipping-and-tracking:events:<EventName>:v1).
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON). Timestamps are RFC 3339
// UTC. None carries an address.

type labelIssuedData struct {
	LabelRef                string `json:"labelRef"`
	TrackingNumber          string `json:"trackingNumber"`
	PONumber                string `json:"poNumber"`
	AssignmentID            string `json:"assignmentId"`
	OrderID                 string `json:"orderId"`
	PackageNo               int    `json:"packageNo"`
	Tier                    string `json:"tier"`
	Carrier                 string `json:"carrier"`
	StopKey                 string `json:"stopKey"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
}

type labelVoidedData struct {
	LabelRef       string `json:"labelRef"`
	TrackingNumber string `json:"trackingNumber"`
	Reason         string `json:"reason"`
}

type packageScannedData struct {
	TrackingNumber string `json:"trackingNumber"`
	ScanID         string `json:"scanId"`
	NodeID         string `json:"nodeId"`
	ScanType       string `json:"scanType"`
	At             string `json:"at"`
	State          string `json:"state"`
}

type packageHandedOverData struct {
	TrackingNumber string `json:"trackingNumber"`
	PONumber       string `json:"poNumber"`
	NodeID         string `json:"nodeId"`
	HandedOverAt   string `json:"handedOverAt"`
}

type packageDeliveredData struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId"`
	OrderID        string `json:"orderId"`
	DeliveredAt    string `json:"deliveredAt"`
}

type packageExceptionData struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId"`
	OrderID        string `json:"orderId"`
	Reason         string `json:"reason"`
	At             string `json:"at"`
}

type packageReturnedData struct {
	TrackingNumber string `json:"trackingNumber"`
	OrderID        string `json:"orderId"`
	ReturnedAt     string `json:"returnedAt"`
}

type packageLostData struct {
	TrackingNumber string `json:"trackingNumber"`
	OrderID        string `json:"orderId"`
	DeclaredAt     string `json:"declaredAt"`
}

// Encoder turns domain events into outbox messages: each event becomes one
// outbox.Message holding the CloudEvents bytes, the Kafka key (the subject) and
// the content-type header. The CloudEvents `id` is minted HERE, once, and
// persisted with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
	// Topic overrides the destination topic (Topic when empty).
	Topic string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	topic := e.Topic
	if topic == "" {
		topic = Topic
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		msg, err := buildMessage(ev, newID(), topic, data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func buildMessage(ev shared.Event, id, topic string, data any) (outbox.Message, error) {
	subject := ev.SubjectID()
	value, err := cloudevents.New(cloudevents.Spec{
		ID:        id,
		Entity:    ev.Entity(),
		EventName: ev.EventName(),
		Subject:   subject,
		Time:      ev.OccurredAt(),
		Stream:    cloudevents.StreamEvents,
		Version:   schemaVersion,
		Data:      data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID:    id,
		Topic:      topic,
		EventType:  cloudevents.Type(ev.Entity(), ev.EventName()),
		Subject:    subject,
		Key:        []byte(subject),
		DataSchema: cloudevents.DataSchema(cloudevents.StreamEvents, ev.EventName(), schemaVersion),
		Value:      value,
		Headers:    []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func payloadFor(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case label.LabelIssued:
		return labelIssuedData{
			LabelRef: string(e.LabelRef), TrackingNumber: string(e.TrackingNumber), PONumber: e.PONumber,
			AssignmentID: e.AssignmentID, OrderID: e.OrderID, PackageNo: e.PackageNo, Tier: string(e.Tier),
			Carrier: e.Carrier, StopKey: e.StopKey, DestinationPostalPrefix: e.DestinationPostalPrefix,
		}, nil
	case label.LabelVoided:
		return labelVoidedData{LabelRef: string(e.LabelRef), TrackingNumber: string(e.TrackingNumber), Reason: e.Reason}, nil
	}
	return journeyPayload(ev)
}

func journeyPayload(ev shared.Event) (any, error) {
	switch e := ev.(type) {
	case journey.PackageScanned:
		return packageScannedData{
			TrackingNumber: string(e.TrackingNumber), ScanID: e.ScanID, NodeID: e.NodeID, ScanType: string(e.ScanType),
			At: ts(e.ScannedAt), State: string(e.State),
		}, nil
	case journey.PackageHandedOver:
		return packageHandedOverData{
			TrackingNumber: string(e.TrackingNumber), PONumber: e.PONumber, NodeID: e.NodeID, HandedOverAt: ts(e.HandedOverAt),
		}, nil
	case journey.PackageDelivered:
		return packageDeliveredData{
			TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID, DeliveredAt: ts(e.DeliveredAt),
		}, nil
	case journey.PackageExceptionRaised:
		return packageExceptionData{
			TrackingNumber: string(e.TrackingNumber), AssignmentID: e.AssignmentID, OrderID: e.OrderID, Reason: e.Reason, At: ts(e.RaisedAt),
		}, nil
	case journey.PackageReturned:
		return packageReturnedData{TrackingNumber: string(e.TrackingNumber), OrderID: e.OrderID, ReturnedAt: ts(e.ReturnedAt)}, nil
	case journey.PackageLost:
		return packageLostData{TrackingNumber: string(e.TrackingNumber), OrderID: e.OrderID, DeclaredAt: ts(e.DeclaredAt)}, nil
	}
	return nil, fmt.Errorf("kafka encoder: %s is not a published event", ev.EventName())
}
