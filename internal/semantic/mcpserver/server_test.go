package mcpserver

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
	"github.com/Teagan42/forge/internal/semantic/toolsurface"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.lsp.dev/protocol"
)

// fakeDriver is a minimal toolsurface.Driver test double, mirroring
// toolsurface's own internal fakeDriver: every method returns its
// pre-configured result.
type fakeDriver struct {
	caps       protocol.ServerCapabilities
	definition []lspdriver.Location
	err        error
}

func (f *fakeDriver) Capabilities() protocol.ServerCapabilities { return f.caps }

func (f *fakeDriver) FindDefinition(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	return f.definition, f.err
}

func (f *fakeDriver) FindReferences(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	return nil, f.err
}

func (f *fakeDriver) FindImplementations(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	return nil, f.err
}

func (f *fakeDriver) SymbolInfo(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.SymbolInfo, error) {
	return lspdriver.SymbolInfo{}, f.err
}

func (f *fakeDriver) DocumentSymbols(ctx context.Context, file string) ([]lspdriver.Symbol, error) {
	return nil, f.err
}

func (f *fakeDriver) WorkspaceSymbols(ctx context.Context, query string) ([]lspdriver.Symbol, error) {
	return nil, f.err
}

func (f *fakeDriver) CallHierarchy(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.CallHierarchy, error) {
	return lspdriver.CallHierarchy{}, f.err
}

func (f *fakeDriver) TypeHierarchy(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.TypeHierarchy, error) {
	return lspdriver.TypeHierarchy{}, f.err
}

// connect wires server to an in-process client over an in-memory
// transport, returning a connected ClientSession the test drives.
func connect(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestNew_RegistersOnlyCapabilityGatedTools(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{
		DefinitionProvider: protocol.Boolean(true),
		HoverProvider:      protocol.Boolean(true),
	}}
	ts := toolsurface.NewToolset(driver, toolsurface.Options{})
	server := New(ts)

	cs := connect(context.Background(), t, server)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	if !got["find_definition"] {
		t.Errorf("tools = %v, want find_definition registered", got)
	}
	if !got["symbol_info"] {
		t.Errorf("tools = %v, want symbol_info registered", got)
	}
	if got["find_references"] {
		t.Errorf("tools = %v, want find_references absent (no ReferencesProvider)", got)
	}
	if len(got) != len(ts.RegisteredTools()) {
		t.Errorf("registered %d tools, want %d matching RegisteredTools()", len(got), len(ts.RegisteredTools()))
	}
}

func TestFindDefinitionTool_ReturnsSourceLocationFromDriver(t *testing.T) {
	driver := &fakeDriver{
		caps: protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(true)},
		definition: []lspdriver.Location{{
			File:     "main.go",
			Position: lspdriver.Position{Line: 10, Column: 2},
		}},
	}
	ts := toolsurface.NewToolset(driver, toolsurface.Options{ReadFile: func(string) ([]byte, error) { return nil, nil }})
	server := New(ts)

	cs := connect(context.Background(), t, server)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "find_definition",
		Arguments: map[string]any{"file": "main.go", "line": 10},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool returned an error result: %+v", res.Content)
	}

	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %#v (%T), want map[string]any", res.StructuredContent, res.StructuredContent)
	}
	if structured["file"] != "main.go" {
		t.Errorf(`structured["file"] = %v, want "main.go"`, structured["file"])
	}
	if structured["line"] != float64(10) {
		t.Errorf(`structured["line"] = %v, want 10`, structured["line"])
	}
}

func TestFindDefinitionTool_DriverErrorSurfacesAsToolError(t *testing.T) {
	driver := &fakeDriver{
		caps: protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(true)},
		err:  context.DeadlineExceeded,
	}
	ts := toolsurface.NewToolset(driver, toolsurface.Options{})
	server := New(ts)

	cs := connect(context.Background(), t, server)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "find_definition",
		Arguments: map[string]any{"file": "main.go", "line": 10},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("CallTool result IsError = false, want true (driver returned an error)")
	}
}
