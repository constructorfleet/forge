package toolsurface

import "context"

// SymbolInfoResult is symbol_info's result: a Source Location plus the
// hover/signature content that isn't itself a location but is always
// emitted alongside one (see ADR-0014 and issue #125's tool signature).
type SymbolInfoResult struct {
	Signature          string          `json:"signature"`
	Type               string          `json:"type"`
	Docs               string          `json:"docs"`
	DefinitionLocation *SourceLocation `json:"definitionLocation,omitempty"`
}

// SymbolInfo resolves the symbol at (file, line) and returns its
// signature/type, documentation, and definition location in one call,
// merging what would otherwise be separate hover and definition requests
// (see ADR-0014). Type mirrors Signature: gopls's hover output doesn't
// distinguish them (see gopls.SymbolInfo's doc comment), so there's nothing
// further to split.
func (t *Toolset) SymbolInfo(ctx context.Context, file string, line int) (SymbolInfoResult, error) {
	pos, err := resolvePosition(ctx, t.driver, file, line, "")
	if err != nil {
		return SymbolInfoResult{}, err
	}

	info, err := t.driver.SymbolInfo(ctx, file, pos)
	if err != nil {
		return SymbolInfoResult{}, err
	}

	result := SymbolInfoResult{
		Signature: info.Signature,
		Type:      info.Signature,
		Docs:      info.Documentation,
	}
	if info.Definition != nil {
		loc := sourceLocationFromGoplsLocation(*info.Definition)
		result.DefinitionLocation = &loc
	}
	return result, nil
}
