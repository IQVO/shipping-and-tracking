package a2a

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const testAgentURL = "http://shipping-and-tracking-a2a.warehouse-systems.svc.cluster.local:8093/"

func testCard() *a2a.AgentCard { return Card(Config{AgentURL: testAgentURL, Version: "1.0.0"}) }

// TestCardShape pins the identity, interface and capability surface of the
// Agent Card: the discovery contract other contexts program against.
func TestCardShape(t *testing.T) {
	card := testCard()

	if card.Name != "shipping-and-tracking" {
		t.Errorf("card name = %q, want shipping-and-tracking", card.Name)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("card has %d interfaces, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.URL != testAgentURL || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("interface = %+v, want JSONRPC at the configured agent URL", iface)
	}
	if card.Capabilities.Streaming {
		t.Error("streaming must not be advertised: this is a message-only agent")
	}
	if card.SecuritySchemes != nil {
		t.Error("card must declare no security schemes: the surface is unauthenticated fleet-wide")
	}
	if card.Provider == nil || card.Provider.Org != "IQVO" {
		t.Errorf("provider = %+v, want IQVO", card.Provider)
	}
	if card.Version != "1.0.0" {
		t.Errorf("card version = %q, want the configured one", card.Version)
	}
}

// TestCardSkillsAreCuratedAndTruthful pins the one-skill, READ-ONLY surface
// (the use cases offer exactly one read) and proves the card never lies about
// its own contract: every advertised example must parse under the SAME strict
// decoder the executor uses, name its own skill, and carry arguments that
// skill accepts.
func TestCardSkillsAreCuratedAndTruthful(t *testing.T) {
	card := testCard()

	if len(card.Skills) != 1 || card.Skills[0].ID != skillGetJourney {
		t.Fatalf("card skills = %+v, want exactly [%s] (curated, goal-shaped, not one per endpoint)", card.Skills, skillGetJourney)
	}
	s := card.Skills[0]
	if s.Name == "" || s.Description == "" || len(s.Examples) == 0 {
		t.Errorf("skill %q lacks a name, description or example", s.ID)
	}
	if !containsTag(s.Tags, "read") || containsTag(s.Tags, "write") {
		t.Errorf("skill %q must be tagged read and never write (this context's MCP surface is read-only), tags = %v", s.ID, s.Tags)
	}
	for _, ex := range s.Examples {
		var req skillRequest
		if err := decodeStrict([]byte(ex), &req); err != nil {
			t.Errorf("skill %q example is not valid under the strict decoder: %v", s.ID, err)
			continue
		}
		if req.Skill != s.ID {
			t.Errorf("skill %q example names skill %q", s.ID, req.Skill)
		}
		if err := decodeStrict(req.Arguments, &getJourneyArgs{}); err != nil {
			t.Errorf("skill %q example arguments are rejected by its own strict decoder: %v", s.ID, err)
		}
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// TestCardNeverClaimsToWrite keeps the card honest about the read-only charter:
// no skill id may name one of the service's write operations, and the card
// must say it is read-only and that it reveals no ship-to address.
func TestCardNeverClaimsToWrite(t *testing.T) {
	card := testCard()
	for _, s := range card.Skills {
		for _, verb := range []string{"issue_", "void_", "record_", "apply_", "return_", "sweep_", "purge_", "create_", "update_", "delete_"} {
			if strings.HasPrefix(s.ID, verb) {
				t.Errorf("skill %q looks like a write; this agent is read-only", s.ID)
			}
		}
	}
	for _, want := range []string{"read-only", "never issues or voids a label", "never records a scan", "never reveals a ship-to address"} {
		if !strings.Contains(card.Description, want) {
			t.Errorf("the card description must say %q: %q", want, card.Description)
		}
	}
}

// TestCardSerializable proves the card marshals to JSON a client can parse
// back, with the discovery fields intact.
func TestCardSerializable(t *testing.T) {
	raw, err := json.Marshal(testCard())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var back a2a.AgentCard
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("card JSON does not round-trip: %v", err)
	}
	if back.Name != "shipping-and-tracking" || len(back.Skills) != 1 {
		t.Errorf("round-tripped card = %q with %d skills", back.Name, len(back.Skills))
	}
}
