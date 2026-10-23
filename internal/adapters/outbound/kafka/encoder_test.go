package kafka

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/outbox"
	"github.com/claudioed/shipping-and-tracking/internal/domain/journey"
	"github.com/claudioed/shipping-and-tracking/internal/domain/label"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

const goldenID = "1b0c9a4e-2f7d-4a63-9d1e-5a7c3e2b9f10"

func fixedID() string { return goldenID }

var (
	commit  = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	handed  = time.Date(2026, 10, 23, 11, 30, 0, 0, time.UTC)
	stopKey = "9f2a6c0d4b7e11aa5c3d8e90b1f47a62c8d0e3f15a9b7c24d6e8f0a1b3c5d7e9"
)

func hdr(v int64) shared.Header { return shared.Header{Version: v, At: commit} }

func envelope(entity, eventName, subject, data string) string {
	return `{"specversion":"1.0","id":"` + goldenID + `","source":"/retailnetwork/shipping-and-tracking",` +
		`"type":"com.retailnetwork.logistics.shipping-and-tracking.` + entity + `.` + eventName + `","subject":"` + subject + `",` +
		`"datacontenttype":"application/json","dataschema":"urn:retailnetwork:shipping-and-tracking:events:` + eventName + `:v1",` +
		`"time":"2026-10-23T12:00:00Z","data":` + data + `}`
}

type goldenCase struct {
	name    string
	event   shared.Event
	entity  string
	subject string
	data    string
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{
			"LabelIssued",
			label.LabelIssued{Header: hdr(1), LabelRef: "lbl-7c1f", TrackingNumber: "RN0123456789ABCDEF", PONumber: "PO-1001",
				AssignmentID: "asg-001", OrderID: "ord-001", PackageNo: 1, Tier: shared.TierNextDay, Carrier: "RN-EXPRESS",
				StopKey: stopKey, DestinationPostalPrefix: "01310"},
			"label", "lbl-7c1f",
			`{"labelRef":"lbl-7c1f","trackingNumber":"RN0123456789ABCDEF","poNumber":"PO-1001","assignmentId":"asg-001","orderId":"ord-001",` +
				`"packageNo":1,"tier":"NEXT_DAY","carrier":"RN-EXPRESS","stopKey":"` + stopKey + `","destinationPostalPrefix":"01310"}`,
		},
		{
			"LabelVoided",
			label.LabelVoided{Header: hdr(2), LabelRef: "lbl-7c1f", TrackingNumber: "RN0123456789ABCDEF", Reason: "order cancelled before pickup"},
			"label", "lbl-7c1f",
			`{"labelRef":"lbl-7c1f","trackingNumber":"RN0123456789ABCDEF","reason":"order cancelled before pickup"}`,
		},
		{
			"PackageScanned",
			journey.PackageScanned{Header: hdr(2), TrackingNumber: "RN0123456789ABCDEF", ScanID: "ev-1:RN0123456789ABCDEF", NodeID: "node-fc-01",
				ScanType: journey.ScanPickupDeparture, ScannedAt: handed, State: journey.StateHandedOver},
			"journey", "RN0123456789ABCDEF",
			`{"trackingNumber":"RN0123456789ABCDEF","scanId":"ev-1:RN0123456789ABCDEF","nodeId":"node-fc-01","scanType":"PICKUP_DEPARTURE",` +
				`"at":"2026-10-23T11:30:00Z","state":"HANDED_OVER"}`,
		},
		{
			"PackageHandedOver",
			journey.PackageHandedOver{Header: hdr(2), TrackingNumber: "RN0123456789ABCDEF", PONumber: "PO-1001", NodeID: "node-fc-01", HandedOverAt: handed},
			"journey", "RN0123456789ABCDEF",
			`{"trackingNumber":"RN0123456789ABCDEF","poNumber":"PO-1001","nodeId":"node-fc-01","handedOverAt":"2026-10-23T11:30:00Z"}`,
		},
		{
			"PackageDelivered",
			journey.PackageDelivered{Header: hdr(7), TrackingNumber: "RN0123456789ABCDEF", AssignmentID: "asg-001", OrderID: "ord-001",
				DeliveredAt: time.Date(2026, 10, 24, 15, 5, 0, 0, time.UTC)},
			"journey", "RN0123456789ABCDEF",
			`{"trackingNumber":"RN0123456789ABCDEF","assignmentId":"asg-001","orderId":"ord-001","deliveredAt":"2026-10-24T15:05:00Z"}`,
		},
		{
			"PackageExceptionRaised",
			journey.PackageExceptionRaised{Header: hdr(4), TrackingNumber: "RN0123456789ABCDEF", AssignmentID: "asg-001", OrderID: "ord-001",
				Reason: "NO_SCAN", RaisedAt: time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)},
			"journey", "RN0123456789ABCDEF",
			`{"trackingNumber":"RN0123456789ABCDEF","assignmentId":"asg-001","orderId":"ord-001","reason":"NO_SCAN","at":"2026-10-24T13:00:00Z"}`,
		},
		{
			"PackageReturned",
			journey.PackageReturned{Header: hdr(8), TrackingNumber: "RN0123456789ABCDEF", OrderID: "ord-001",
				ReturnedAt: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)},
			"journey", "RN0123456789ABCDEF",
			`{"trackingNumber":"RN0123456789ABCDEF","orderId":"ord-001","returnedAt":"2026-10-26T09:00:00Z"}`,
		},
		{
			"PackageLost",
			journey.PackageLost{Header: hdr(5), TrackingNumber: "RN0123456789ABCDEF", OrderID: "ord-001",
				DeclaredAt: time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC)},
			"journey", "RN0123456789ABCDEF",
			`{"trackingNumber":"RN0123456789ABCDEF","orderId":"ord-001","declaredAt":"2026-10-27T13:00:00Z"}`,
		},
	}
}

// TestEncoder_GoldenWireFormat pins the exact bytes of every published type
// (all CloudEvents attributes + payload, as apis/asyncapi.yaml's examples) and
// the outbox row around them (content-type header, key, topic, full type,
// dataschema, id).
func TestEncoder_GoldenWireFormat(t *testing.T) {
	enc := &Encoder{NewID: fixedID}
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			msgs, err := enc.Encode(c.event)
			if err != nil || len(msgs) != 1 {
				t.Fatalf("Encode: %v (%d messages)", err, len(msgs))
			}
			want := envelope(c.entity, c.name, c.subject, c.data)
			if string(msgs[0].Value) != want {
				t.Fatalf("value:\n got %s\nwant %s", msgs[0].Value, want)
			}
			assertRow(t, msgs[0], c)
		})
	}
}

func assertRow(t *testing.T, m outbox.Message, c goldenCase) {
	t.Helper()
	if m.EventID != goldenID || m.Topic != "retailnetwork.shipping-and-tracking.events" || m.Subject != c.subject ||
		string(m.Key) != c.subject {
		t.Errorf("row identity = %s %s %s %s", m.EventID, m.Topic, m.Subject, m.Key)
	}
	if m.EventType != "com.retailnetwork.logistics.shipping-and-tracking."+c.entity+"."+c.name ||
		m.DataSchema != "urn:retailnetwork:shipping-and-tracking:events:"+c.name+":v1" {
		t.Errorf("row type = %s %s", m.EventType, m.DataSchema)
	}
	if len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != "application/cloudevents+json; charset=UTF-8" {
		t.Errorf("headers = %+v", m.Headers)
	}
}

func TestEncoder_EncodesInOrderWithAFreshIDPerEvent(t *testing.T) {
	n := 0
	enc := &Encoder{NewID: func() string { n++; return "id-" + string(rune('0'+n)) }, Topic: "t.override"}
	var evs []shared.Event
	for _, c := range goldenCases()[:3] {
		evs = append(evs, c.event)
	}
	msgs, err := enc.Encode(evs...)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("Encode: %v %d", err, len(msgs))
	}
	for i, m := range msgs {
		if m.EventID != "id-"+string(rune('1'+i)) || m.Topic != "t.override" {
			t.Errorf("message %d = %s %s", i, m.EventID, m.Topic)
		}
	}
	if !strings.HasSuffix(msgs[0].EventType, "label.LabelIssued") || !strings.HasSuffix(msgs[2].EventType, "journey.PackageScanned") {
		t.Errorf("order not kept: %s ... %s", msgs[0].EventType, msgs[2].EventType)
	}
}

func TestEncoder_DefaultsMintUUIDsAndUseTheServiceTopic(t *testing.T) {
	msgs, err := NewEncoder().Encode(goldenCases()[1].event)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs[0].EventID) != 36 || msgs[0].Topic != Topic {
		t.Fatalf("id=%q topic=%q", msgs[0].EventID, msgs[0].Topic)
	}
	msgs, err = (&Encoder{}).Encode(goldenCases()[1].event)
	if err != nil || len(msgs[0].EventID) != 36 || msgs[0].Topic != Topic {
		t.Fatalf("zero Encoder must default: %v %+v", err, msgs)
	}
	again, _ := NewEncoder().Encode(goldenCases()[1].event)
	if again[0].EventID == msgs[0].EventID {
		t.Fatal("ids must be fresh")
	}
}

type alien struct{ shared.Header }

func (alien) EventName() string { return "Alien" }
func (alien) Entity() string    { return "journey" }
func (alien) SubjectID() string { return "x" }

func TestEncoder_RefusesAnUnpublishedEventAndAnEmptySubject(t *testing.T) {
	if _, err := NewEncoder().Encode(alien{hdr(1)}); err == nil || !strings.Contains(err.Error(), "not a published event") {
		t.Fatalf("want a not-published error, got %v", err)
	}
	empty := label.LabelVoided{Header: hdr(2), TrackingNumber: "RN1", Reason: "x"} // LabelRef "" -> empty subject
	if _, err := NewEncoder().Encode(empty); err == nil {
		t.Fatal("an event with an empty subject must fail")
	}
}

func TestEncoder_NoPayloadCarriesAnAddressField(t *testing.T) {
	for _, c := range goldenCases() {
		msgs, err := NewEncoder().Encode(c.event)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{`"line1"`, `"city"`, `"postalCode"`, `"name"`, `"phone"`, `"address"`} {
			if strings.Contains(string(msgs[0].Value), banned) {
				t.Errorf("%s carries %s", c.name, banned)
			}
		}
	}
}
