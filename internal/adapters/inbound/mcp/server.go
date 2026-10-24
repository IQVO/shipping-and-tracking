// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use case the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0006-mcp-server-adoption.md the surface is READ-ONLY: the one
// tool reads journey data (get_journey), exactly the query GET
// /journeys/{trackingNumber} serves; issuing and voiding labels and recording
// scans stay on REST and on the event consumers, and governance_test.go fails
// the build on a write-verb tool name. This package depends inward on the
// application layer and the domain only, never on an outbound adapter or the
// REST adapter, and nothing else may depend on it (internal/architecture's
// TestMCPAdapterDependencyRule; TestMCPServerIsReadOnly pins that no write
// use case is reachable). The composition root (cmd/mcp) wires the concrete
// repository into the use case. There is no auth of any kind (fleet-wide
// revert 2026-09-11; TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "shipping-and-tracking-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Shipping and tracking of the retail network (read-only): read the customer-visible " +
	"journey of one package with get_journey (its tracking number) -- the state (LABELLED, HANDED_OVER, " +
	"IN_TRANSIT, AT_STATION, OUT_FOR_DELIVERY, DELIVERED, RETURNED, LOST or VOIDED) and every stored scan, " +
	"applied or not, in the order they were recorded. Nothing here changes data: issuing and voiding labels " +
	"and recording scans are REST operations of shipping-and-tracking, and the journey is otherwise moved by " +
	"the events the service consumes."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
