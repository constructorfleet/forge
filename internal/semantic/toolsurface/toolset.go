package toolsurface

import (
	"context"
	"os"

	"github.com/Teagan42/forge/internal/semantic/gopls"
	"go.lsp.dev/protocol"
)

// defaultMaxResults is the list-result cap applied when Options.MaxResults
// is left at its zero value, matching internal/config's lsp.max_results
// default.
const defaultMaxResults = 50

// Driver is the subset of *gopls.Driver this package calls into, named for
// testability: a fake satisfying this interface stands in for a live gopls
// subprocess in tests.
type Driver interface {
	Capabilities() protocol.ServerCapabilities
	FindDefinition(ctx context.Context, file string, pos gopls.Position) ([]gopls.Location, error)
	FindReferences(ctx context.Context, file string, pos gopls.Position) ([]gopls.Location, error)
	FindImplementations(ctx context.Context, file string, pos gopls.Position) ([]gopls.Location, error)
	SymbolInfo(ctx context.Context, file string, pos gopls.Position) (gopls.SymbolInfo, error)
	DocumentSymbols(ctx context.Context, file string) ([]gopls.Symbol, error)
	WorkspaceSymbols(ctx context.Context, query string) ([]gopls.Symbol, error)
	CallHierarchy(ctx context.Context, file string, pos gopls.Position) (gopls.CallHierarchy, error)
	TypeHierarchy(ctx context.Context, file string, pos gopls.Position) (gopls.TypeHierarchy, error)
}

// var _ Driver ensures *gopls.Driver keeps satisfying this interface as
// gopls.Driver evolves.
var _ Driver = (*gopls.Driver)(nil)

// Options configures a Toolset.
type Options struct {
	// MaxResults caps every list-returning tool's result count. Non-positive
	// values fall back to defaultMaxResults, matching internal/config's
	// lsp.max_results default of 50 (see ADR-0014).
	MaxResults int

	// ReadFile reads a source file's contents for find_definition's inlined
	// snippet. Defaults to os.ReadFile; overridable so tests don't need a
	// real filesystem.
	ReadFile func(path string) ([]byte, error)
}

// Toolset is the Forge-managed, agent-facing tool set backed by a single
// Driver: one method per tool named in ADR-0014, each capability-gated
// against the Driver's advertised capabilities.
type Toolset struct {
	driver     Driver
	maxResults int
	readFile   func(path string) ([]byte, error)
}

// NewToolset returns a Toolset backed by driver.
func NewToolset(driver Driver, opts Options) *Toolset {
	max := opts.MaxResults
	if max <= 0 {
		max = defaultMaxResults
	}
	readFile := opts.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	return &Toolset{driver: driver, maxResults: max, readFile: readFile}
}

// RegisteredTools returns the names of the tools this Toolset registers,
// given the Driver's currently advertised capabilities — the intersection
// of {all seven tools} and {server initialize capabilities} from ADR-0014.
// A capability the server didn't advertise is simply absent from this list
// rather than registered-and-failing.
func (t *Toolset) RegisteredTools() []string {
	caps := t.driver.Capabilities()

	var names []string
	if providerEnabled(caps.DefinitionProvider) {
		names = append(names, "find_definition")
	}
	if providerEnabled(caps.ReferencesProvider) {
		names = append(names, "find_references")
	}
	if providerEnabled(caps.ImplementationProvider) {
		names = append(names, "find_implementations")
	}
	if providerEnabled(caps.HoverProvider) {
		names = append(names, "symbol_info")
	}
	if providerEnabled(caps.DocumentSymbolProvider) || providerEnabled(caps.WorkspaceSymbolProvider) {
		names = append(names, "search_symbols")
	}
	if providerEnabled(caps.CallHierarchyProvider) {
		names = append(names, "call_hierarchy")
	}
	if providerEnabled(caps.TypeHierarchyProvider) {
		names = append(names, "type_hierarchy")
	}
	return names
}

// providerEnabled reports whether a *Provider capability field (a union of
// nil, protocol.Boolean, or an options pointer) indicates the server
// supports the capability. nil and an explicit Boolean(false) both mean
// unsupported; any other value (Boolean(true) or an options pointer) means
// supported.
func providerEnabled(v any) bool {
	if v == nil {
		return false
	}
	if b, ok := v.(protocol.Boolean); ok {
		return bool(b)
	}
	return true
}
