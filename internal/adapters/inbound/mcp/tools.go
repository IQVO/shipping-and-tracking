package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
	"github.com/claudioed/shipping-and-tracking/internal/application/views"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use case the REST adapter uses (GET
// /journeys/{trackingNumber}); the adapter never constructs an outbound
// adapter itself, and it holds no write use case at all (docs/adr/0006).
// POST /labels, POST /labels/{labelRef}:void and POST
// /journeys/{trackingNumber}/scans stay REST-only.
type Deps struct {
	GetJourney *usecases.GetJourney
}

// --- inputs -------------------------------------------------------------------

type trackingNumberInput struct {
	TrackingNumber string `json:"trackingNumber" jsonschema:"the tracking number of the package, e.g. RN0123456789ABCDEF"`
}

// --- outputs (the REST body, camelCase like the REST contract) ----------------

type scanView struct {
	ScanID   string `json:"scanId"`
	NodeID   string `json:"nodeId"`
	ScanType string `json:"scanType"`
	At       string `json:"at"`
	Applied  bool   `json:"applied"`
}

type journeyView struct {
	TrackingNumber string     `json:"trackingNumber"`
	State          string     `json:"state"`
	PONumber       string     `json:"poNumber"`
	AssignmentID   string     `json:"assignmentId"`
	OrderID        string     `json:"orderId"`
	Tier           string     `json:"tier"`
	HandedOverAt   string     `json:"handedOverAt,omitempty"`
	DeliveredAt    string     `json:"deliveredAt,omitempty"`
	Scans          []scanView `json:"scans"`
}

// --- mapping ------------------------------------------------------------------

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func toJourney(v views.Journey) journeyView {
	out := journeyView{
		TrackingNumber: v.TrackingNumber, State: v.State, PONumber: v.PONumber,
		AssignmentID: v.AssignmentID, OrderID: v.OrderID, Tier: v.Tier,
		Scans: make([]scanView, 0, len(v.Scans)),
	}
	for _, s := range v.Scans {
		out.Scans = append(out.Scans, scanView{
			ScanID: s.ScanID, NodeID: s.NodeID, ScanType: s.ScanType,
			At: instant(s.At), Applied: s.Applied,
		})
	}
	if v.HandedOverAt != nil {
		out.HandedOverAt = instant(*v.HandedOverAt)
	}
	if v.DeliveredAt != nil {
		out.DeliveredAt = instant(*v.DeliveredAt)
	}
	return out
}

// --- handlers -----------------------------------------------------------------

func (d Deps) getJourney(ctx context.Context, in trackingNumberInput) (journeyView, error) {
	v, err := d.GetJourney.Execute(ctx, shared.TrackingNumber(in.TrackingNumber))
	if err != nil {
		return journeyView{}, mapError(err)
	}
	return toJourney(v), nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the one read-only tool. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0006).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	addTool(server, &mcp.Tool{
		Name: "get_journey",
		Description: "Read the customer-visible journey of one package by tracking number: the state (LABELLED, " +
			"HANDED_OVER, IN_TRANSIT, AT_STATION, OUT_FOR_DELIVERY, DELIVERED, RETURNED, LOST or VOIDED), the " +
			"poNumber, assignmentId, orderId and tier of its label, handedOverAt and deliveredAt when they " +
			"happened, and every stored scan in the order they were recorded -- each with its scanType, the " +
			"node it was seen at, when, and applied (false for a scan stored out of order or without an edge " +
			"from the state it met). Read-only; an unknown tracking number fails with not-found.",
		Annotations: readOnly,
	}, d.getJourney)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
