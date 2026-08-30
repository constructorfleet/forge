package lspdriver

import "go.lsp.dev/protocol"

// Position is a location within a file in this package's typed shape: both
// Line and Column are 1-based, matching how callers name a source location
// ("file:line") and how editors report cursor position. LSP itself is
// 0-based on both axes; toLSPPosition/fromLSPPosition are the only place
// that conversion happens, so every other method in this package works
// exclusively in 1-based coordinates.
type Position struct {
	Line   int
	Column int
}

// toLSPPosition converts a 1-based Position to the 0-based protocol.Position
// the LSP wire format requires.
func toLSPPosition(p Position) protocol.Position {
	return protocol.Position{
		Line:      uint32(p.Line - 1),
		Character: uint32(p.Column - 1),
	}
}

// fromLSPPosition converts a 0-based protocol.Position back to this
// package's 1-based Position.
func fromLSPPosition(p protocol.Position) Position {
	return Position{
		Line:   int(p.Line) + 1,
		Column: int(p.Character) + 1,
	}
}
