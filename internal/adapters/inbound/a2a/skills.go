package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/shipping-and-tracking/internal/application/ports"
	"github.com/claudioed/shipping-and-tracking/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/shipping-and-tracking/internal/adapters/inbound/a2a"

// skillGetJourney is the skill id advertised on the Agent Card (see Card). It
// is the MCP tool name of the same read (ADR 0006), so one name identifies one
// question across both agent-facing surfaces.
const skillGetJourney = "get_journey"

// skillRequest is the wire contract of every skill call: the first meaningful
// message part carries (as a Data part, or a Text part holding the same JSON)
// the skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored. (Go's
// encoding/json matches field names case-insensitively, so "trackingnumber"
// still reaches trackingNumber; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// getJourneyArgs is the argument shape of get_journey.
type getJourneyArgs struct {
	TrackingNumber string `json:"trackingNumber"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (docs/adr/0009).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillGetJourney:
		data, problem := deps.getJourney(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{Journey: &data, summary: summarizeJourney(data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first
// meaningful part. A Data part (or a Text part holding the same JSON object)
// is accepted; anything else is a request-shape problem, answered, not
// raised, because a malformed delegation is a business answer for the calling
// agent, not a transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// getJourney is the package-tracking read skill, through the same GetJourney
// use case the REST and MCP adapters use.
//
// The tracking number is trimmed like the REST path parameter is. One that is
// not a well-formed tracking number (shared.TrackingNumber.Valid: 1 to 64
// characters of a restricted alphabet, which excludes U+0000) can never name
// a journey, because every journey is started from a validated one; it is
// answered 404 not-found without a database round trip, the answer REST gives
// for the same value in the path. That is also what keeps a NUL character from
// reaching a Postgres text parameter (SQLSTATE 22021, a 500), as the REST
// adapter's rejectNUL does.
func (d Deps) getJourney(ctx context.Context, rawArgs json.RawMessage) (journeyDTO, *problem) {
	var args getJourneyArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return journeyDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	tn := shared.TrackingNumber(strings.TrimSpace(args.TrackingNumber))
	if tn == "" {
		return journeyDTO{}, problemInvalidArgument("trackingNumber is required")
	}
	if !tn.Valid() {
		return journeyDTO{}, problemNotFound("")
	}
	v, err := d.GetJourney.Execute(ctx, tn)
	if err != nil {
		return journeyDTO{}, problemFromUseCase(ctx, err, string(tn))
	}
	return toJourneyDTO(v), nil
}

// problemFromUseCase maps a use case error to a problem answer, using the SAME
// classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: a missing journey is 404
// not-found, a value-object rejection is 422 validation-failed and anything
// unrecognised is 500 internal-error. The detail is adapter-owned text, so the
// shared error's wording never reaches an agent client and an infrastructure
// error (a DSN, SQL) is logged, never answered. Domain rejections are EXPECTED
// business outcomes and are answered as RFC 7807-shaped problems; anything
// unrecognised is still answered as a problem, so the calling agent always
// receives a structured, machine-readable result.
func problemFromUseCase(ctx context.Context, err error, trackingNumber string) *problem {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return problemNotFound(trackingNumber)
	case errors.Is(err, shared.ErrValidation):
		return problemValidationFailed("the tracking number is not valid")
	default:
		slog.ErrorContext(ctx, "a2a skill failed", "error", err)
		return problemInternal()
	}
}
