package toolsurface

import "context"

// FindReferences resolves the symbol at (file, line[, symbol]) and returns
// its references, location-only and capped at t.maxResults (see ADR-0014).
//
// The underlying Driver always includes the declaration in its result (see
// gopls.Driver.FindReferences); when includeDeclaration is false, this
// method excludes it itself by comparing against the symbol's definition
// location, since the driver has no per-call toggle for it.
func (t *Toolset) FindReferences(ctx context.Context, file string, line int, symbol string, includeDeclaration bool) (ListResult, error) {
	pos, err := resolvePosition(ctx, t.driver, file, line, symbol)
	if err != nil {
		return ListResult{}, err
	}

	refs, err := t.driver.FindReferences(ctx, file, pos)
	if err != nil {
		return ListResult{}, err
	}

	if !includeDeclaration {
		defs, err := t.driver.FindDefinition(ctx, file, pos)
		if err != nil {
			return ListResult{}, err
		}
		refs = excludeLocations(refs, defs)
	}

	locs := make([]SourceLocation, 0, len(refs))
	for _, r := range refs {
		locs = append(locs, sourceLocationFromGoplsLocation(r))
	}
	return capLocations(locs, t.maxResults), nil
}

// FindImplementations resolves the symbol at (file, line[, symbol]) and
// returns its implementations, location-only and capped at t.maxResults.
func (t *Toolset) FindImplementations(ctx context.Context, file string, line int, symbol string) (ListResult, error) {
	pos, err := resolvePosition(ctx, t.driver, file, line, symbol)
	if err != nil {
		return ListResult{}, err
	}

	impls, err := t.driver.FindImplementations(ctx, file, pos)
	if err != nil {
		return ListResult{}, err
	}

	locs := make([]SourceLocation, 0, len(impls))
	for _, i := range impls {
		locs = append(locs, sourceLocationFromGoplsLocation(i))
	}
	return capLocations(locs, t.maxResults), nil
}
