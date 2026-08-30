// Package toolsurface is the Forge-managed, agent-facing tool set for
// backends without their own native semantic-navigation tool (the Codex
// path today; see ADR-0014). It consolidates the underlying LSP request set
// into one tool per Semantic Capability, normalizes every location-bearing
// result to Source Location, and hard-caps list results.
package toolsurface

import "github.com/Teagan42/forge/internal/semantic/lspdriver"

// SourceLocation is the common, 1-based result shape every location-
// returning tool in this package normalizes to (see ADR-0014 and CONTEXT.md's
// Source Location glossary entry).
type SourceLocation struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Col        int    `json:"col,omitempty"`
	EndLine    int    `json:"endLine,omitempty"`
	SymbolName string `json:"symbolName,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
}

// sourceLocationFromLSPLocation normalizes a lspdriver.Location, which carries
// no symbol name or kind of its own.
func sourceLocationFromLSPLocation(loc lspdriver.Location) SourceLocation {
	return SourceLocation{
		File: loc.File,
		Line: loc.Position.Line,
		Col:  loc.Position.Column,
	}
}

// sourceLocationFromLSPSymbol normalizes a lspdriver.Symbol, carrying its name
// and kind alongside the location.
func sourceLocationFromLSPSymbol(sym lspdriver.Symbol) SourceLocation {
	loc := sourceLocationFromLSPLocation(sym.Location)
	loc.SymbolName = sym.Name
	loc.Kind = sym.Kind
	return loc
}
