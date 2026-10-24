package architecture

// Read-only MCP sensor (docs/adr/0006, ported from task-interleaving's
// internal/architecture/mcp_readonly_fitness_test.go). The MCP server is an
// ADDITIVE, READ-ONLY inbound adapter: it can read a package journey and can
// never change it or emit an event. "Read-only" is pinned structurally, not
// by review:
//
//   - the adapter's Deps struct holds only the read use case;
//   - the adapter imports no write-capable seam (the outbox, an event sink,
//     a unit of work, a Kafka adapter, the memory doubles, the REST adapter);
//   - cmd/mcp wires only that use case and imports none of the write side.
//
// One deviation from the reference, on purpose: package ports is NOT on the
// forbidden list here, because the read-error vocabulary the tool errors are
// mapped from lives in it (ports.ErrNotFound, the same sentinel REST maps to
// not-found). The Deps check keeps every other port out of the adapter.
//
// TestMCPReadOnlySensorBites proves each detector flags what it should.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// readUseCases is the whole set of use cases the MCP surface may hold or wire.
var readUseCases = map[string]bool{"GetJourney": true}

// mcpAllowedUsecaseRefs are the non-use-case names of package usecases the
// MCP sources may refer to: the read output. Everything else in package
// usecases is a write path (or a seam to one) and is refused.
var mcpAllowedUsecaseRefs = map[string]bool{
	"GetJourney": true,
	"Deps":       true, // the use-case Deps bundle GetJourney embeds
}

// mcpForbiddenImportParts are import-path fragments no MCP source may carry:
// the write-capable seams and every moving part of the write side.
var mcpForbiddenImportParts = []string{
	"/internal/application/outbox",
	"/internal/adapters/outbound/outbox",
	"/internal/adapters/outbound/kafka",
	"/internal/adapters/outbound/memory",
	"/internal/adapters/outbound/customerordering",
	"/internal/adapters/outbound/crypto",
	"/internal/adapters/inbound/kafka",
	"/internal/adapters/inbound/http",
	"/internal/scheduler",
	"segmentio/kafka-go",
}

// mcpImportViolations returns one finding per forbidden import of one source.
func mcpImportViolations(filename string, src any) ([]string, error) {
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
		for _, part := range mcpForbiddenImportParts {
			if strings.Contains(path, part) {
				out = append(out, fmt.Sprintf("%s imports %s", filename, path))
			}
		}
	}
	return out, nil
}

// depsFieldViolations returns every field of the struct named Deps in src
// whose type is not *usecases.<read use case>.
func depsFieldViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Deps" {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			if name, ok := usecaseName(field.Type); ok && readUseCases[name] {
				continue
			}
			names := make([]string, 0, len(field.Names))
			for _, id := range field.Names {
				names = append(names, id.Name)
			}
			out = append(out, fmt.Sprintf("%s: Deps field %s is not a read use case", filename, strings.Join(names, ",")))
		}
		return false
	})
	return out, nil
}

// usecaseName returns N for the type expression *usecases.N.
func usecaseName(e ast.Expr) (string, bool) {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return "", false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "usecases" {
		return "", false
	}
	return sel.Sel.Name, true
}

// wiredUseCaseViolations returns every usecases.<Name> a source refers to
// that is not in mcpAllowedUsecaseRefs (the MCP sources must not even name a
// write use case).
func wiredUseCaseViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "usecases" && !mcpAllowedUsecaseRefs[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s refers to usecases.%s, which is not a read use case or a read input/output", filename, sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

func mcpReadOnlyFiles(t *testing.T) (adapter, command []string) {
	t.Helper()
	adapter = goFilesUnder(t, "../adapters/inbound/mcp", false)
	command = goFilesUnder(t, filepath.Join("..", "..", "cmd", "mcp"), false)
	if len(adapter) == 0 || len(command) == 0 {
		t.Skip("no internal/adapters/inbound/mcp or no cmd/mcp in this service: nothing to check")
	}
	sort.Strings(adapter)
	sort.Strings(command)
	return adapter, command
}

// TestMCPServerIsReadOnly: the MCP adapter and cmd/mcp can reach no write
// path.
func TestMCPServerIsReadOnly(t *testing.T) {
	adapter, command := mcpReadOnlyFiles(t)
	for _, path := range append(append([]string{}, adapter...), command...) {
		found, err := mcpImportViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- the MCP surface may read through the use cases only (docs/adr/0006)", v)
		}
	}
	for _, path := range adapter {
		found, err := depsFieldViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- the MCP adapter may hold read use cases only (docs/adr/0006)", v)
		}
	}
	for _, path := range append(append([]string{}, adapter...), command...) {
		found, err := wiredUseCaseViolations(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range found {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- wire only the read use cases (docs/adr/0006)", v)
		}
	}
}

func TestMCPReadOnlySensorBites(t *testing.T) {
	const module = "github.com/claudioed/shipping-and-tracking"
	imports := []struct {
		name string
		src  string
		want int
	}{
		{"the outbox relay is flagged", "package x\nimport \"" + module + "/internal/adapters/outbound/outbox\"", 1},
		{"a kafka writer is flagged", "package x\nimport \"" + module + "/internal/adapters/outbound/kafka\"", 1},
		{"a kafka consumer is flagged", "package x\nimport \"" + module + "/internal/adapters/inbound/kafka\"", 1},
		{"kafka-go is flagged", "package x\nimport \"github.com/segmentio/kafka-go\"", 1},
		{"the memory doubles are flagged", "package x\nimport \"" + module + "/internal/adapters/outbound/memory\"", 1},
		{"the REST adapter is flagged", "package x\nimport \"" + module + "/internal/adapters/inbound/http\"", 1},
		{"usecases and ports pass", "package x\nimport (\n\"" + module + "/internal/application/usecases\"\n\"" + module + "/internal/application/ports\"\n)", 0},
		{"the postgres adapter passes", "package x\nimport \"" + module + "/internal/adapters/outbound/postgres\"", 0},
	}
	for _, tc := range imports {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpImportViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	deps := []struct {
		name string
		src  string
		want int
	}{
		{"a write use case field is flagged", "package x\ntype Deps struct{ Issue *usecases.IssueLabel }", 1},
		{"a unit of work field is flagged", "package x\ntype Deps struct{ UoW ports.UnitOfWork }", 1},
		{"a non-pointer read use case is flagged", "package x\ntype Deps struct{ Get usecases.GetJourney }", 1},
		{"the read use case passes", "package x\ntype Deps struct{ Get *usecases.GetJourney }", 0},
		{"other structs are not Deps", "package x\ntype Other struct{ W *usecases.IssueLabel }", 0},
	}
	for _, tc := range deps {
		t.Run(tc.name, func(t *testing.T) {
			got, err := depsFieldViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}

	wired := []struct {
		name string
		src  string
		want int
	}{
		{"constructing a write use case is flagged", "package main\nvar _ = &usecases.IssueLabel{}", 1},
		{"naming the scan writer is flagged", "package main\nvar _ *usecases.RecordScan", 1},
		{"the read use case passes", "package main\nvar _ = &usecases.GetJourney{}", 0},
	}
	for _, tc := range wired {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wiredUseCaseViolations("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}
}
