package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/shipping-and-tracking"

// labelsIssuedName is the Tier 2 business metric of the context (ADR 0008, the
// fleet's standard metrics convention): every label-issue attempt, by outcome.
const labelsIssuedName = "shipping.labels.issued"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeIssued   = "issued"
	outcomeRejected = "rejected"
)

// LabelMetrics implements ports.LabelMetrics against the global MeterProvider.
// Until Setup installs a real provider the global one is a no-op, so recording
// is cheap and safe in tests and local runs.
type LabelMetrics struct {
	counter metric.Int64Counter
}

var _ ports.LabelMetrics = (*LabelMetrics)(nil)

// NewLabelMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewLabelMetrics() (*LabelMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		labelsIssuedName,
		metric.WithDescription("Label-issue attempts by outcome (issued or rejected; reason says why a rejection happened, "+
			"as a problem slug such as ship-to-unavailable or label-already-issued). "+
			"A falling issued share with a rising ship-to-unavailable means labels are blocked because customer-ordering "+
			"cannot be reached, so packages are not entering the network; a rising label-already-issued means the caller is "+
			"retrying without an Idempotency-Key; a rising internal-error means the service itself is failing."),
		metric.WithUnit("{label}"),
	)
	if err != nil {
		return nil, err
	}
	return &LabelMetrics{counter: counter}, nil
}

// LabelIssued counts a label that was issued.
func (m *LabelMetrics) LabelIssued(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeIssued)))
}

// LabelRejected counts an attempt that issued no label, with the reason why.
func (m *LabelMetrics) LabelRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
