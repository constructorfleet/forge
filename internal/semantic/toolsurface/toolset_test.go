package toolsurface

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
	"go.lsp.dev/protocol"
)

// fakeDriver is a test double for Driver: every method returns its
// pre-configured result, recording the arguments it was called with.
type fakeDriver struct {
	caps protocol.ServerCapabilities

	definition     []lspdriver.Location
	references     []lspdriver.Location
	implementation []lspdriver.Location
	symbolInfo     lspdriver.SymbolInfo
	documentSyms   []lspdriver.Symbol
	workspaceSyms  []lspdriver.Symbol
	callHierarchy  lspdriver.CallHierarchy
	typeHierarchy  lspdriver.TypeHierarchy
	err            error
}

func (f *fakeDriver) Capabilities() protocol.ServerCapabilities { return f.caps }

func (f *fakeDriver) FindDefinition(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	return f.definition, f.err
}

func (f *fakeDriver) FindReferences(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	return f.references, f.err
}

func (f *fakeDriver) FindImplementations(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	return f.implementation, f.err
}

func (f *fakeDriver) SymbolInfo(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.SymbolInfo, error) {
	return f.symbolInfo, f.err
}

func (f *fakeDriver) DocumentSymbols(ctx context.Context, file string) ([]lspdriver.Symbol, error) {
	return f.documentSyms, f.err
}

func (f *fakeDriver) WorkspaceSymbols(ctx context.Context, query string) ([]lspdriver.Symbol, error) {
	return f.workspaceSyms, f.err
}

func (f *fakeDriver) CallHierarchy(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.CallHierarchy, error) {
	return f.callHierarchy, f.err
}

func (f *fakeDriver) TypeHierarchy(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.TypeHierarchy, error) {
	return f.typeHierarchy, f.err
}

func TestNewToolset_DefaultsMaxResultsWhenNonPositive(t *testing.T) {
	ts := NewToolset(&fakeDriver{}, Options{MaxResults: 0})

	if ts.maxResults != defaultMaxResults {
		t.Fatalf("maxResults = %d, want default %d", ts.maxResults, defaultMaxResults)
	}
}

func TestNewToolset_UsesConfiguredMaxResults(t *testing.T) {
	ts := NewToolset(&fakeDriver{}, Options{MaxResults: 10})

	if ts.maxResults != 10 {
		t.Fatalf("maxResults = %d, want 10", ts.maxResults)
	}
}

func TestRegisteredTools_BaselineToolsAlwaysRegisteredWhenAdvertised(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{
		DefinitionProvider:      protocol.Boolean(true),
		ReferencesProvider:      protocol.Boolean(true),
		ImplementationProvider:  protocol.Boolean(true),
		HoverProvider:           protocol.Boolean(true),
		DocumentSymbolProvider:  protocol.Boolean(true),
		WorkspaceSymbolProvider: protocol.Boolean(true),
	}}
	ts := NewToolset(driver, Options{})

	got := ts.RegisteredTools()

	want := []string{"find_definition", "find_references", "find_implementations", "symbol_info", "search_symbols"}
	assertSameTools(t, got, want)
}

func TestRegisteredTools_HierarchyToolsGatedByCapability(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{
		CallHierarchyProvider: protocol.Boolean(true),
	}}
	ts := NewToolset(driver, Options{})

	got := ts.RegisteredTools()

	assertContains(t, got, "call_hierarchy")
	assertNotContains(t, got, "type_hierarchy")
}

func TestRegisteredTools_UnadvertisedCapabilityNotRegistered(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{}}
	ts := NewToolset(driver, Options{})

	got := ts.RegisteredTools()

	assertNotContains(t, got, "call_hierarchy")
	assertNotContains(t, got, "type_hierarchy")
	assertNotContains(t, got, "find_definition")
}

func TestRegisteredTools_ExplicitFalseBooleanNotRegistered(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{
		DefinitionProvider: protocol.Boolean(false),
	}}
	ts := NewToolset(driver, Options{})

	got := ts.RegisteredTools()

	assertNotContains(t, got, "find_definition")
}

func assertContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if g == want {
			return
		}
	}
	t.Fatalf("RegisteredTools() = %v, want to contain %q", got, want)
}

func assertNotContains(t *testing.T, got []string, unwanted string) {
	t.Helper()
	for _, g := range got {
		if g == unwanted {
			t.Fatalf("RegisteredTools() = %v, want NOT to contain %q", got, unwanted)
		}
	}
}

func assertSameTools(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("RegisteredTools() = %v, want %v", got, want)
	}
	for _, w := range want {
		assertContains(t, got, w)
	}
}
