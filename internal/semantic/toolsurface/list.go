package toolsurface

import "github.com/Teagan42/forge/internal/semantic/lspdriver"

// ListResult is the shape every list-returning tool responds with: the
// (possibly capped) items, whether the underlying result was truncated to
// reach the cap, and the untruncated total. There is no cursor-based
// pagination in v1 (see ADR-0014) — a truncated caller either narrows its
// query/scope or accepts the top slice.
type ListResult struct {
	Items     []SourceLocation `json:"items"`
	Truncated bool             `json:"truncated"`
	Total     int              `json:"total"`
}

// capLocations caps items at max, reporting the untruncated total and
// whether truncation occurred. items are assumed already sorted by the
// caller's native ranking; capLocations only slices, it never reorders.
func capLocations(items []SourceLocation, max int) ListResult {
	total := len(items)
	if total <= max {
		return ListResult{Items: items, Total: total}
	}
	return ListResult{Items: items[:max], Truncated: true, Total: total}
}

// symbolLocations normalizes a []lspdriver.Symbol into Source Locations.
func symbolLocations(syms []lspdriver.Symbol) []SourceLocation {
	locs := make([]SourceLocation, 0, len(syms))
	for _, s := range syms {
		locs = append(locs, sourceLocationFromLSPSymbol(s))
	}
	return locs
}

// excludeLocations returns the locs in from that don't match any of the
// file:line:column positions in exclude.
func excludeLocations(from, exclude []lspdriver.Location) []lspdriver.Location {
	if len(exclude) == 0 {
		return from
	}
	skip := make(map[lspdriver.Location]struct{}, len(exclude))
	for _, e := range exclude {
		skip[e] = struct{}{}
	}

	out := make([]lspdriver.Location, 0, len(from))
	for _, l := range from {
		if _, found := skip[l]; found {
			continue
		}
		out = append(out, l)
	}
	return out
}
