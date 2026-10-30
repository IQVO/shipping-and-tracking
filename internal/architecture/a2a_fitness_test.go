package architecture

// A2A sensors (docs/adr/0009). The A2A server is an ADDITIVE, READ-ONLY
// inbound adapter, exactly like the MCP server (docs/adr/0006): it can read a
// package journey and can never change it or emit an event, and it is a
// SERVER only. Both are pinned structurally, not by review:
//
//   - the adapter depends on the application and domain layers only, and
//     nothing else in internal/ depends on it (TestA2AAdapterDependencyRule);
//   - the adapter imports no write-capable seam, its Deps holds only the read
//     use case, and neither it nor cmd/a2a names another use case. These are
//     the detectors of mcp_readonly_fitness_test.go, applied to the A2A
//     sources (TestA2AServerIsReadOnly);
//   - neither contains an outbound A2A client, so this context can never
//     delegate to a sibling (TestA2AHasNoOutboundClient).
//
// TestA2ASensorBites proves the A2A-specific detector flags what it should.

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// a2aClientImportPart is the import-path fragment of the SDK's outbound
// client package.
const a2aClientImportPart = "a2a-go/v2/a2aclient"

// TestA2AAdapterDependencyRule: the A2A inbound adapter is additive, never
// load-bearing. It may depend only on the application and domain layers
// (never on outbound adapters, never on cmd), and nothing else in this
// codebase may depend on it; cmd/a2a wires it up and nothing else touches it.
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

func a2aSourceFiles(t *testing.T) (adapter, command []string) {
	t.Helper()
	adapter = goFilesUnder(t, "../adapters/inbound/a2a", false)
	command = goFilesUnder(t, filepath.Join("..", "..", "cmd", "a2a"), false)
	if len(adapter) == 0 || len(command) == 0 {
		t.Fatal("no internal/adapters/inbound/a2a or no cmd/a2a sources found; the sensor is looking in the wrong place")
	}
	sort.Strings(adapter)
	sort.Strings(command)
	return adapter, command
}

// TestA2AServerIsReadOnly: the A2A adapter and cmd/a2a can reach no write
// path. This context's MCP charter is entirely read-only (ADR 0006), so the
// A2A agent derived from it is read-only too and never widens the surface.
func TestA2AServerIsReadOnly(t *testing.T) {
	adapter, command := a2aSourceFiles(t)
	all := append(append([]string{}, adapter...), command...)
	for _, path := range all {
		for _, check := range []func(string, any) ([]string, error){mcpImportViolations, wiredUseCaseViolations} {
			found, err := check(path, nil)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, v := range found {
				t.Errorf("READ-ONLY A2A VIOLATION: %s -- the A2A surface may read through GetJourney only (docs/adr/0009)", v)
			}
		}
	}
	for _, path := range adapter {
		found, err := depsFieldViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY A2A VIOLATION: %s -- the A2A adapter may hold read use cases only (docs/adr/0009)", v)
		}
	}
}

// a2aClientViolations returns one finding per import of the SDK's outbound
// client package in one source.
func a2aClientViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		if strings.Contains(path, a2aClientImportPart) {
			out = append(out, fmt.Sprintf("%s imports %s", filename, path))
		}
	}
	return out, nil
}

// TestA2AHasNoOutboundClient: this agent is a server only. It never delegates
// to a sibling context, so no non-test source holds an A2A client (the wire
// tests legitimately drive the agent with the SDK client).
func TestA2AHasNoOutboundClient(t *testing.T) {
	adapter, command := a2aSourceFiles(t)
	for _, path := range append(append([]string{}, adapter...), command...) {
		found, err := a2aClientViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("OUTBOUND A2A CLIENT: %s -- shipping-and-tracking serves A2A, it never calls a sibling (docs/adr/0009)", v)
		}
	}
}

func TestA2ASensorBites(t *testing.T) {
	const module = "github.com/claudioed/shipping-and-tracking"
	clients := []struct {
		name string
		src  string
		want int
	}{
		{"the SDK client is flagged", "package x\nimport \"github.com/a2aproject/a2a-go/v2/a2aclient\"", 1},
		{"an aliased SDK client is flagged", "package x\nimport c \"github.com/a2aproject/a2a-go/v2/a2aclient\"", 1},
		{"the SDK server passes", "package x\nimport (\n\"github.com/a2aproject/a2a-go/v2/a2a\"\n\"github.com/a2aproject/a2a-go/v2/a2asrv\"\n)", 0},
	}
	for _, tc := range clients {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a2aClientViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	// The shared read-only detectors must bite on A2A-shaped sources too: a
	// write use case in Deps, a write use case wired in cmd/a2a, the outbox
	// or the REST adapter imported.
	writes := []struct {
		name  string
		check func(string, any) ([]string, error)
		src   string
	}{
		{"a write use case in Deps", depsFieldViolations, "package a2a\ntype Deps struct{ Issue *usecases.IssueLabel }"},
		{"a unit of work in Deps", depsFieldViolations, "package a2a\ntype Deps struct{ UoW ports.UnitOfWork }"},
		{"a write use case wired in cmd/a2a", wiredUseCaseViolations, "package main\nvar _ = &usecases.RecordScan{}"},
		{"the outbox imported", mcpImportViolations, "package x\nimport \"" + module + "/internal/adapters/outbound/outbox\""},
		{"the REST adapter imported", mcpImportViolations, "package x\nimport \"" + module + "/internal/adapters/inbound/http\""},
		{"kafka-go imported", mcpImportViolations, "package x\nimport \"github.com/segmentio/kafka-go\""},
	}
	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.check("src.go", tc.src)
			if err != nil || len(got) != 1 {
				t.Fatalf("findings = %v (err %v), want exactly 1", got, err)
			}
		})
	}
}
