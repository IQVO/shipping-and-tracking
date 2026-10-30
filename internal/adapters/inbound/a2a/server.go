// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// same application-layer read use case the HTTP and MCP adapters use. It is
// built on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0
// over HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0009-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between bounded contexts: an agent client sends a goal-shaped skill request
// as a structured message part and this agent answers from its own state
// through its own use case. It is not a data-sync channel: facts between
// contexts still travel exclusively as CloudEvents on Kafka (ADR 0004),
// published through the transactional outbox.
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0006): its one
// skill reads the customer-visible journey of a package. Issuing and voiding
// labels and recording scans stay on REST and on the event consumers. This
// package never writes, holds no write use case, no unit of work and no event
// publisher, and contains no outbound A2A client. The ship-to address never
// leaves the encrypted snapshot (ADR 0002 A9) and the journey view carries no
// address field, so no answer can.
//
// Like the MCP adapter, this package depends inward on the application layer
// and the domain only, never on an outbound adapter or the REST adapter; the
// composition root (cmd/a2a) wires the concrete repository into the use case.
// Domain structs never leak across the message boundary.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/shipping-and-tracking/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skill needs, injected by the composition root.
// It carries the SAME read use case the HTTP and MCP adapters use (GET
// /journeys/{trackingNumber}); the adapter never constructs an outbound
// adapter itself and holds no write use case at all (docs/adr/0009).
type Deps struct {
	// GetJourney is the existing read use case, reused unchanged: the
	// customer-visible timeline of one package.
	GetJourney *usecases.GetJourney
}

// Card builds this agent's Agent Card (docs/adr/0009). The surface is one
// curated, goal-shaped READ skill derived from the MCP tool get_journey
// (docs/adr/0006): this context's MCP charter is entirely read-only, so the
// agent is read-only too and never widens what the context exposes. The use
// cases offer exactly one read, so the agent offers exactly one skill. The
// card declares no security schemes on purpose: this surface is
// unauthenticated by the same fleet-wide decision as REST and MCP; schemes are
// added here, not in a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name: "shipping-and-tracking",
		Description: "Shipping & Tracking agent of the retail network: the read-only source of truth for where a " +
			"package is. It answers the customer-visible journey of one package by tracking number: its state, " +
			"the label's poNumber, assignmentId, orderId and tier, when it was handed over and delivered, and " +
			"every stored scan. It never issues or voids a label, never records a scan, never reveals a ship-to " +
			"address, and does not decide routing or delivery.",
		Version: cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/shipping-and-tracking",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:   skillGetJourney,
				Name: "Track one package",
				Description: "Read the customer-visible journey of one package by its trackingNumber: the state " +
					"(LABELLED, HANDED_OVER, IN_TRANSIT, AT_STATION, OUT_FOR_DELIVERY, DELIVERED, RETURNED, LOST " +
					"or VOIDED), the poNumber, assignmentId, orderId and tier of its label, handedOverAt and " +
					"deliveredAt when they happened, and every stored scan in the order they were recorded, each " +
					"with its scanType, nodeId, when, and applied (false for a scan stored out of order or without " +
					"an edge from the state it met). Answers a 404 problem when no journey has that tracking " +
					"number. Persists nothing.",
				Tags:       []string{"shipping", "tracking", "journey", "read"},
				InputModes: []string{"application/json"},
				Examples: []string{
					`{"skill":"get_journey","arguments":{"trackingNumber":"RN0123456789ABCDEF"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): the curated skill is a transactional request/response read, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (docs/adr/0009), mirroring ADR 0006's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
