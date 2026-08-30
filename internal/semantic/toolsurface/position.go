package toolsurface

import (
	"context"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

// resolvePosition turns a tool's (file, line, symbol?) input — this
// package's position-based tools take a line rather than a column, since
// the agent rarely knows the exact column — into the lspdriver.Position the
// Driver's methods require.
//
// If symbol is given, resolvePosition looks it up among the line's document
// symbols and uses its column. Otherwise, if exactly one symbol occupies the
// line, its column is used since there's no ambiguity to resolve. In every
// other case (no symbol given and zero-or-multiple symbols on the line, or
// symbol given but not found on the line) it falls back to column 1: a
// wrong-but-safe guess a request to line's start would still exercise most
// LSP position handling for.
func resolvePosition(ctx context.Context, driver Driver, file string, line int, symbol string) (lspdriver.Position, error) {
	syms, err := driver.DocumentSymbols(ctx, file)
	if err != nil {
		return lspdriver.Position{}, err
	}

	var onLine []lspdriver.Symbol
	for _, s := range syms {
		if s.Location.Position.Line == line {
			onLine = append(onLine, s)
		}
	}

	if symbol != "" {
		for _, s := range onLine {
			if s.Name == symbol {
				return s.Location.Position, nil
			}
		}
		return lspdriver.Position{Line: line, Column: 1}, nil
	}

	if len(onLine) == 1 {
		return onLine[0].Location.Position, nil
	}

	return lspdriver.Position{Line: line, Column: 1}, nil
}
