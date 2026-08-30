package toolsurface

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
	"go.lsp.dev/protocol"
)

// testExtensions is the extension -> language table the router tests route
// through, mirroring the shape cmd/forge builds from the Language Server
// Registry.
var testExtensions = map[string]string{
	".go": "go",
	".py": "python",
	".rs": "rust",
}

// pyrightLikeDriver stands in for a driver whose server never advertised
// implementationProvider (pyright): every other call behaves like the
// embedded fake, but FindImplementations degrades exactly the way
// lspdriver.Driver's own capability gate does.
type pyrightLikeDriver struct {
	fakeDriver
}

func (p *pyrightLikeDriver) FindImplementations(context.Context, string, lspdriver.Position) ([]lspdriver.Location, error) {
	return nil, lspdriver.ErrCapabilityUnsupported
}

func TestRouter_FindDefinitionRoutesByExtension(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go":     &fakeDriver{definition: []lspdriver.Location{{File: "from-go-driver.go"}}},
		"python": &fakeDriver{definition: []lspdriver.Location{{File: "from-python-driver.py"}}},
	}, testExtensions)

	got, err := router.FindDefinition(context.Background(), "pkg/service.py", lspdriver.Position{Line: 1})
	if err != nil {
		t.Fatalf("FindDefinition() error = %v, want nil", err)
	}
	if len(got) != 1 || got[0].File != "from-python-driver.py" {
		t.Fatalf("FindDefinition() = %+v, want the python driver's result", got)
	}
}

func TestRouter_UnknownExtensionYieldsErrNoDriverForFile(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go": &fakeDriver{definition: []lspdriver.Location{{File: "from-go-driver.go"}}},
	}, testExtensions)

	_, err := router.FindDefinition(context.Background(), "README.md", lspdriver.Position{})
	if !errors.Is(err, ErrNoDriverForFile) {
		t.Fatalf("FindDefinition() error = %v, want ErrNoDriverForFile", err)
	}
}

func TestRouter_KnownExtensionWithNoStartedDriverYieldsErrNoDriverForFile(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go": &fakeDriver{definition: []lspdriver.Location{{File: "from-go-driver.go"}}},
	}, testExtensions)

	_, err := router.FindDefinition(context.Background(), "pkg/service.py", lspdriver.Position{})
	if !errors.Is(err, ErrNoDriverForFile) {
		t.Fatalf("FindDefinition() error = %v, want ErrNoDriverForFile (python detected in the table but no driver started)", err)
	}
}

// TestRouter_CapabilitiesUnionsAcrossDrivers pins the capability model a
// multiplexing server needs: Toolset.RegisteredTools takes a single
// Capabilities snapshot, so a tool must be registered when *any* driver
// supports it — per-call degradation is what handles the drivers that
// don't (see TestRouter_FindImplementationsDegradesPerDriver).
func TestRouter_CapabilitiesUnionsAcrossDrivers(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go": &fakeDriver{caps: protocol.ServerCapabilities{
			ImplementationProvider: protocol.Boolean(true),
			TypeHierarchyProvider:  protocol.Boolean(true),
		}},
		"python": &fakeDriver{caps: protocol.ServerCapabilities{
			HoverProvider:          protocol.Boolean(true),
			DocumentSymbolProvider: protocol.Boolean(true),
			ImplementationProvider: protocol.Boolean(false),
		}},
	}, testExtensions)

	got := NewToolset(router, Options{}).RegisteredTools()

	assertSameTools(t, got, []string{"find_implementations", "type_hierarchy", "symbol_info", "search_symbols"})
}

// TestRouter_WorkspaceSymbolsMergesAcrossDrivers is the second half of
// acceptance criterion 2: a workspace-symbol search fans out to every
// driver and merges, in a stable language order.
func TestRouter_WorkspaceSymbolsMergesAcrossDrivers(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go":     &fakeDriver{workspaceSyms: []lspdriver.Symbol{{Name: "GoSymbol"}}},
		"python": &fakeDriver{workspaceSyms: []lspdriver.Symbol{{Name: "PySymbol"}}},
		"rust":   &fakeDriver{workspaceSyms: []lspdriver.Symbol{{Name: "RsSymbol"}}},
	}, testExtensions)

	got, err := router.WorkspaceSymbols(context.Background(), "Symbol")
	if err != nil {
		t.Fatalf("WorkspaceSymbols() error = %v, want nil", err)
	}

	want := []string{"GoSymbol", "PySymbol", "RsSymbol"}
	if len(got) != len(want) {
		t.Fatalf("WorkspaceSymbols() = %+v, want %v merged across all three drivers", got, want)
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("WorkspaceSymbols()[%d].Name = %q, want %q (stable language order)", i, got[i].Name, w)
		}
	}
}

func TestRouter_WorkspaceSymbolsSkipsFailingDriver(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go":     &fakeDriver{workspaceSyms: []lspdriver.Symbol{{Name: "GoSymbol"}}},
		"python": &fakeDriver{err: lspdriver.ErrInert},
	}, testExtensions)

	got, err := router.WorkspaceSymbols(context.Background(), "Symbol")
	if err != nil {
		t.Fatalf("WorkspaceSymbols() error = %v, want nil: an inert driver must not fail the whole search", err)
	}
	if len(got) != 1 || got[0].Name != "GoSymbol" {
		t.Fatalf("WorkspaceSymbols() = %+v, want only the healthy driver's result", got)
	}
}

func TestRouter_WorkspaceSymbolsSurfacesErrorWhenEveryDriverFails(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go":     &fakeDriver{err: lspdriver.ErrInert},
		"python": &fakeDriver{err: lspdriver.ErrInert},
	}, testExtensions)

	if _, err := router.WorkspaceSymbols(context.Background(), "Symbol"); !errors.Is(err, lspdriver.ErrInert) {
		t.Fatalf("WorkspaceSymbols() error = %v, want ErrInert: no driver could answer at all", err)
	}
}

// TestRouter_FileScopedToolsRouteByExtension covers every remaining
// file-scoped tool: each must reach the driver serving the file's language,
// never a sibling driver.
func TestRouter_FileScopedToolsRouteByExtension(t *testing.T) {
	goDriver := &fakeDriver{
		references:    []lspdriver.Location{{File: "ref.go"}},
		symbolInfo:    lspdriver.SymbolInfo{Signature: "func Go()"},
		documentSyms:  []lspdriver.Symbol{{Name: "GoSymbol"}},
		callHierarchy: lspdriver.CallHierarchy{Item: lspdriver.HierarchyItem{Name: "GoCall"}},
		typeHierarchy: lspdriver.TypeHierarchy{Item: lspdriver.HierarchyItem{Name: "GoType"}},
	}
	pyDriver := &fakeDriver{
		references:    []lspdriver.Location{{File: "ref.py"}},
		symbolInfo:    lspdriver.SymbolInfo{Signature: "def py()"},
		documentSyms:  []lspdriver.Symbol{{Name: "PySymbol"}},
		callHierarchy: lspdriver.CallHierarchy{Item: lspdriver.HierarchyItem{Name: "PyCall"}},
		typeHierarchy: lspdriver.TypeHierarchy{Item: lspdriver.HierarchyItem{Name: "PyType"}},
	}
	router := NewRouter(map[string]Driver{"go": goDriver, "python": pyDriver}, testExtensions)
	ctx := context.Background()
	pos := lspdriver.Position{Line: 3}

	refs, err := router.FindReferences(ctx, "svc/service.py", pos)
	if err != nil || len(refs) != 1 || refs[0].File != "ref.py" {
		t.Fatalf("FindReferences(.py) = %+v, %v, want the python driver's result", refs, err)
	}

	info, err := router.SymbolInfo(ctx, "svc/service.go", pos)
	if err != nil || info.Signature != "func Go()" {
		t.Fatalf("SymbolInfo(.go) = %+v, %v, want the go driver's result", info, err)
	}

	syms, err := router.DocumentSymbols(ctx, "svc/service.py")
	if err != nil || len(syms) != 1 || syms[0].Name != "PySymbol" {
		t.Fatalf("DocumentSymbols(.py) = %+v, %v, want the python driver's result", syms, err)
	}

	calls, err := router.CallHierarchy(ctx, "svc/service.go", pos)
	if err != nil || calls.Item.Name != "GoCall" {
		t.Fatalf("CallHierarchy(.go) = %+v, %v, want the go driver's result", calls, err)
	}

	types, err := router.TypeHierarchy(ctx, "svc/service.py", pos)
	if err != nil || types.Item.Name != "PyType" {
		t.Fatalf("TypeHierarchy(.py) = %+v, %v, want the python driver's result", types, err)
	}
}

// TestRouter_FindImplementationsDegradesPerDriver is acceptance criterion 3:
// the routed driver's own ErrCapabilityUnsupported reaches the caller
// unchanged for a pyright-served file, while the drivers that do advertise
// the capability keep resolving.
func TestRouter_FindImplementationsDegradesPerDriver(t *testing.T) {
	router := NewRouter(map[string]Driver{
		"go":     &fakeDriver{implementation: []lspdriver.Location{{File: "impl.go"}}},
		"rust":   &fakeDriver{implementation: []lspdriver.Location{{File: "impl.rs"}}},
		"python": &pyrightLikeDriver{},
	}, testExtensions)
	ctx := context.Background()

	if _, err := router.FindImplementations(ctx, "svc/service.py", lspdriver.Position{}); !errors.Is(err, lspdriver.ErrCapabilityUnsupported) {
		t.Fatalf("FindImplementations(.py) error = %v, want lspdriver.ErrCapabilityUnsupported", err)
	}

	for _, tc := range []struct{ file, want string }{
		{"svc/service.go", "impl.go"},
		{"svc/service.rs", "impl.rs"},
	} {
		got, err := router.FindImplementations(ctx, tc.file, lspdriver.Position{})
		if err != nil {
			t.Fatalf("FindImplementations(%q) error = %v, want nil", tc.file, err)
		}
		if len(got) != 1 || got[0].File != tc.want {
			t.Fatalf("FindImplementations(%q) = %+v, want %q", tc.file, got, tc.want)
		}
	}
}
