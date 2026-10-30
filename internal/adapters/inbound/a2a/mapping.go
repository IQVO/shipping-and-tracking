package a2a

import (
	"fmt"
	"net/http"
	"time"

	"github.com/claudioed/shipping-and-tracking/internal/application/views"
)

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope. Type
// is the problem slug, the SAME slug the REST adapter uses as its RFC 9457
// "type" (apis/openapi.yaml, Problem.type) for the outcomes both surfaces
// share, so a delegating agent sees the vocabulary a REST caller would.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

// Problems the adapter itself raises before any use case runs: the request is
// not a skill request, names a skill this agent does not offer, or carries
// arguments the skill does not accept. REST has no such outcome (its routes
// are fixed), so these are plain 400s.
func problemMalformedRequest(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: http.StatusBadRequest}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{
		Type: "unknown-skill", Title: "Unknown skill", Status: http.StatusBadRequest,
		Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill),
	}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: http.StatusBadRequest}
}

// The use-case outcomes, classified exactly like the REST adapter's
// problemCatalogue (internal/adapters/inbound/http/errors.go) for the errors a
// read can raise. The 409 and 503 classes of REST belong to writes and to the
// ship-to lookup, neither of which this read-only agent reaches.
func problemNotFound(trackingNumber string) *problem {
	detail := "no journey exists for this tracking number"
	if trackingNumber != "" {
		detail = fmt.Sprintf("no journey exists for tracking number %q", trackingNumber)
	}
	return &problem{Type: "not-found", Title: "Resource not found", Detail: detail, Status: http.StatusNotFound}
}

func problemValidationFailed(detail string) *problem {
	return &problem{Type: "validation-failed", Title: "Validation failed", Detail: detail, Status: http.StatusUnprocessableEntity}
}

func problemInternal() *problem {
	return &problem{
		Type: "internal-error", Title: "Internal server error", Status: http.StatusInternalServerError,
		Detail: "the skill failed; see server logs (trace id in the response metadata)",
	}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	Journey *journeyDTO `json:"journey,omitempty"`
	summary string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// scanDTO is the message-boundary representation of one stored scan.
type scanDTO struct {
	ScanID   string `json:"scanId"`
	NodeID   string `json:"nodeId"`
	ScanType string `json:"scanType"`
	At       string `json:"at"`
	Applied  bool   `json:"applied"`
}

// journeyDTO is the message-boundary representation of a package journey:
// exactly the fields the application view exposes, the same set the MCP tool
// and the REST wire carry. There is no address field, because the view has
// none: the ship-to never leaves its encrypted snapshot (ADR 0002 A9).
type journeyDTO struct {
	TrackingNumber string    `json:"trackingNumber"`
	State          string    `json:"state"`
	PONumber       string    `json:"poNumber"`
	AssignmentID   string    `json:"assignmentId"`
	OrderID        string    `json:"orderId"`
	Tier           string    `json:"tier"`
	HandedOverAt   string    `json:"handedOverAt,omitempty"`
	DeliveredAt    string    `json:"deliveredAt,omitempty"`
	Scans          []scanDTO `json:"scans"`
}

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// toJourneyDTO folds the application view into the boundary DTO.
func toJourneyDTO(v views.Journey) journeyDTO {
	out := journeyDTO{
		TrackingNumber: v.TrackingNumber, State: v.State, PONumber: v.PONumber,
		AssignmentID: v.AssignmentID, OrderID: v.OrderID, Tier: v.Tier,
		Scans: make([]scanDTO, 0, len(v.Scans)),
	}
	for _, s := range v.Scans {
		out.Scans = append(out.Scans, scanDTO{
			ScanID: s.ScanID, NodeID: s.NodeID, ScanType: s.ScanType, At: instant(s.At), Applied: s.Applied,
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

// summarizeJourney is the one-line text form of one journey.
func summarizeJourney(j journeyDTO) string {
	applied := 0
	for _, s := range j.Scans {
		if s.Applied {
			applied++
		}
	}
	return fmt.Sprintf("%s: %s, %d scans (%d applied)", j.TrackingNumber, j.State, len(j.Scans), applied)
}
