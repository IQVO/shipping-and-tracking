package kafka

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/shipping-and-tracking/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/shipping-and-tracking/internal/analytics/report"
)

// The CloudEvents entity segments of this context's two aggregates.
const (
	entityLabel   = "label"
	entityJourney = "journey"
)

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0012) to its report.Kind.
func projectedKinds() map[string]report.Kind {
	return map[string]report.Kind{
		cloudevents.Type(entityLabel, string(report.KindLabelIssued)):        report.KindLabelIssued,
		cloudevents.Type(entityJourney, string(report.KindPackageScanned)):   report.KindPackageScanned,
		cloudevents.Type(entityJourney, string(report.KindPackageDelivered)): report.KindPackageDelivered,
		cloudevents.Type(entityJourney, string(report.KindPackageReturned)):  report.KindPackageReturned,
		cloudevents.Type(entityJourney, string(report.KindPackageLost)):      report.KindPackageLost,
	}
}

// acknowledgedTypes are this context's other published types: committed past
// without a warning, because no report needs them (ADR 0012).
func acknowledgedTypes() map[string]bool {
	return map[string]bool{
		cloudevents.Type(entityLabel, "LabelVoided"):              true,
		cloudevents.Type(entityJourney, "PackageHandedOver"):      true,
		cloudevents.Type(entityJourney, "PackageExceptionRaised"): true,
	}
}

// analyticsData is the union of the payload fields the projection reads,
// hand-mirrored from apis/asyncapi.yaml (never imported from the encoder).
// Unknown fields are ignored; an absent timestamp stays zero.
type analyticsData struct {
	LabelRef       string    `json:"labelRef"`
	TrackingNumber string    `json:"trackingNumber"`
	Tier           string    `json:"tier"`
	ScanID         string    `json:"scanId"`
	ScanType       string    `json:"scanType"`
	At             time.Time `json:"at"`
	DeliveredAt    time.Time `json:"deliveredAt"`
	ReturnedAt     time.Time `json:"returnedAt"`
	DeclaredAt     time.Time `json:"declaredAt"`
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	subjectOf, occurredAt := d.TrackingNumber, d.instant(kind, evt.Time())
	if kind == report.KindLabelIssued {
		subjectOf = d.LabelRef
	}
	if subjectOf == "" || subjectOf != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), subjectOf, evt.Subject())
	}
	e := report.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), TrackingNumber: d.TrackingNumber, OccurredAt: occurredAt.UTC(),
		LabelRef: d.LabelRef, Tier: d.Tier, ScanID: d.ScanID, ScanType: d.ScanType,
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}

// instant is the business instant of a fact: the scan's `at`, the outcome's
// own timestamp, or the CloudEvents time of a label (LabelIssued has none).
func (d analyticsData) instant(kind report.Kind, eventTime time.Time) time.Time {
	switch kind {
	case report.KindPackageScanned:
		return d.At
	case report.KindPackageDelivered:
		return d.DeliveredAt
	case report.KindPackageReturned:
		return d.ReturnedAt
	case report.KindPackageLost:
		return d.DeclaredAt
	default:
		return eventTime
	}
}
