package toolsurface

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
	"go.lsp.dev/protocol"
)

// ErrNoDriverForFile is returned by a file-scoped Router method when the
// file's extension maps to no language, or to a language no driver was
// started for. It is a degradation, not a failure of the tool surface: the
// workspace simply has no Language Server that serves that file.
var ErrNoDriverForFile = errors.New("toolsurface: no language server for file")

// Router multiplexes one Driver per language behind the single Driver
// interface a Toolset binds to (see ADR 0016). File-scoped calls dispatch to
// the driver serving the file argument's extension; WorkspaceSymbols fans
// out to every driver and merges.
type Router struct {
	drivers    map[string]Driver
	extensions map[string]string
}

// var _ Driver ensures *Router stays pluggable into a Toolset with no
// Toolset change — the seam multiplexing rests on.
var _ Driver = (*Router)(nil)

// NewRouter returns a Router dispatching over drivers, keyed by the language
// identifiers of the Language Server Registry ("go", "python", …), using
// extensions as the file-extension -> language table. Extension keys are
// normalized to lowercase with a leading dot, so ".PY", "py", and ".py" are
// all accepted.
func NewRouter(drivers map[string]Driver, extensions map[string]string) *Router {
	normalized := make(map[string]string, len(extensions))
	for ext, language := range extensions {
		normalized[normalizeExtension(ext)] = strings.ToLower(language)
	}
	return &Router{drivers: drivers, extensions: normalized}
}

// FindDefinition routes to the driver serving file's extension.
func (r *Router) FindDefinition(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return nil, err
	}
	return driver.FindDefinition(ctx, file, pos)
}

// Capabilities returns the union of every driver's advertised capabilities:
// a capability any driver supports is reported as supported, so
// Toolset.RegisteredTools — which snapshots capabilities once — registers a
// tool as soon as one language can answer it. The drivers that cannot are
// handled per call, where the routed driver's own capability gate returns
// lspdriver.ErrCapabilityUnsupported.
//
// Only the fields the tool surface gates on are unioned; the rest of the
// protocol's capabilities have no meaning above a single connection.
func (r *Router) Capabilities() protocol.ServerCapabilities {
	// Each field is normalized to Boolean(true) rather than carried over
	// verbatim: an options pointer describes one server's connection, which
	// has no meaning for a merged, multi-server surface, while "some driver
	// can answer this" is exactly what the tool surface reads.
	on := protocol.Boolean(true)

	var merged protocol.ServerCapabilities
	for _, language := range r.languages() {
		caps := r.drivers[language].Capabilities()
		if providerEnabled(caps.DefinitionProvider) {
			merged.DefinitionProvider = on
		}
		if providerEnabled(caps.ReferencesProvider) {
			merged.ReferencesProvider = on
		}
		if providerEnabled(caps.ImplementationProvider) {
			merged.ImplementationProvider = on
		}
		if providerEnabled(caps.HoverProvider) {
			merged.HoverProvider = on
		}
		if providerEnabled(caps.DocumentSymbolProvider) {
			merged.DocumentSymbolProvider = on
		}
		if providerEnabled(caps.WorkspaceSymbolProvider) {
			merged.WorkspaceSymbolProvider = on
		}
		if providerEnabled(caps.CallHierarchyProvider) {
			merged.CallHierarchyProvider = on
		}
		if providerEnabled(caps.TypeHierarchyProvider) {
			merged.TypeHierarchyProvider = on
		}
	}
	return merged
}

// FindReferences routes to the driver serving file's extension.
func (r *Router) FindReferences(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return nil, err
	}
	return driver.FindReferences(ctx, file, pos)
}

// SymbolInfo routes to the driver serving file's extension.
func (r *Router) SymbolInfo(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.SymbolInfo, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return lspdriver.SymbolInfo{}, err
	}
	return driver.SymbolInfo(ctx, file, pos)
}

// DocumentSymbols routes to the driver serving file's extension, so each
// language's own ServerProfile (e.g. pyright's DropSymbolChildren) applies.
func (r *Router) DocumentSymbols(ctx context.Context, file string) ([]lspdriver.Symbol, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return nil, err
	}
	return driver.DocumentSymbols(ctx, file)
}

// CallHierarchy routes to the driver serving file's extension.
func (r *Router) CallHierarchy(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.CallHierarchy, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return lspdriver.CallHierarchy{}, err
	}
	return driver.CallHierarchy(ctx, file, pos)
}

// TypeHierarchy routes to the driver serving file's extension.
func (r *Router) TypeHierarchy(ctx context.Context, file string, pos lspdriver.Position) (lspdriver.TypeHierarchy, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return lspdriver.TypeHierarchy{}, err
	}
	return driver.TypeHierarchy(ctx, file, pos)
}

// FindImplementations routes to the driver serving file's extension. A
// routed driver that never advertised the capability returns its own
// lspdriver.ErrCapabilityUnsupported, which reaches the caller unchanged —
// one language degrading never affects the others (see ADR 0016).
func (r *Router) FindImplementations(ctx context.Context, file string, pos lspdriver.Position) ([]lspdriver.Location, error) {
	driver, err := r.driverFor(file)
	if err != nil {
		return nil, err
	}
	return driver.FindImplementations(ctx, file, pos)
}

// WorkspaceSymbols fans out to every driver and merges their results, in
// language order so the merged list is stable across calls. A driver that
// errors (e.g. a language whose server never came up and is inert) is
// skipped rather than failing the whole search — per ADR 0016 one language
// degrading must not take the others with it. If every driver errors, the
// first error is returned, so a total failure is still surfaced.
func (r *Router) WorkspaceSymbols(ctx context.Context, query string) ([]lspdriver.Symbol, error) {
	var (
		merged   []lspdriver.Symbol
		firstErr error
		ok       bool
	)
	for _, language := range r.languages() {
		symbols, err := r.drivers[language].WorkspaceSymbols(ctx, query)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = true
		merged = append(merged, symbols...)
	}
	if !ok && firstErr != nil {
		return nil, firstErr
	}
	return merged, nil
}

// languages returns this Router's driver languages in sorted order, giving
// every fan-out a stable, reproducible result order.
func (r *Router) languages() []string {
	languages := make([]string, 0, len(r.drivers))
	for language := range r.drivers {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// driverFor resolves file's extension to the driver serving its language,
// returning ErrNoDriverForFile when the extension maps to no started driver.
func (r *Router) driverFor(file string) (Driver, error) {
	ext := normalizeExtension(filepath.Ext(file))
	language, ok := r.extensions[ext]
	if !ok {
		return nil, fmt.Errorf("%w: %q (extension %q maps to no language)", ErrNoDriverForFile, file, ext)
	}
	driver, ok := r.drivers[language]
	if !ok {
		return nil, fmt.Errorf("%w: %q (no %s language server started)", ErrNoDriverForFile, file, language)
	}
	return driver, nil
}

// normalizeExtension lowercases ext and ensures a single leading dot, so
// registry and operator-configured extension tables can be written either
// way.
func normalizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		return "." + ext
	}
	return ext
}
