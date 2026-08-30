package toolsurface

import (
	"context"
	"fmt"
)

// Scope selects search_symbols's search space.
type Scope string

const (
	// ScopeFile searches one file's document symbols.
	ScopeFile Scope = "file"
	// ScopeWorkspace searches the whole workspace's symbols.
	ScopeWorkspace Scope = "workspace"
)

// SearchSymbols searches for symbols matching query, merging document- and
// workspace-symbol search behind one parameterized tool (see ADR-0014).
// file is required when scope is ScopeFile and ignored otherwise.
//
// Note: query is not applied client-side for ScopeFile — gopls's
// documentSymbol request has no query parameter, so a file-scoped search
// returns that file's full symbol list for the caller to filter.
func (t *Toolset) SearchSymbols(ctx context.Context, query string, scope Scope, file string) (ListResult, error) {
	switch scope {
	case ScopeFile:
		if file == "" {
			return ListResult{}, fmt.Errorf("toolsurface: search_symbols scope %q requires file", ScopeFile)
		}
		results, err := t.driver.DocumentSymbols(ctx, file)
		if err != nil {
			return ListResult{}, err
		}
		return capLocations(symbolLocations(results), t.maxResults), nil
	case ScopeWorkspace:
		results, err := t.driver.WorkspaceSymbols(ctx, query)
		if err != nil {
			return ListResult{}, err
		}
		return capLocations(symbolLocations(results), t.maxResults), nil
	default:
		return ListResult{}, fmt.Errorf("toolsurface: unsupported search_symbols scope %q; supported: %s, %s", scope, ScopeFile, ScopeWorkspace)
	}
}
