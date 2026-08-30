package toolsurface

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func symAt(name string, line, col int) lspdriver.Symbol {
	return lspdriver.Symbol{Name: name, Location: lspdriver.Location{File: "f.go", Position: lspdriver.Position{Line: line, Column: col}}}
}

func TestResolvePosition_NoSymbolsOnLineDefaultsToColumnOne(t *testing.T) {
	driver := &fakeDriver{documentSyms: []lspdriver.Symbol{symAt("Foo", 3, 5)}}

	pos, err := resolvePosition(context.Background(), driver, "f.go", 10, "")
	if err != nil {
		t.Fatalf("resolvePosition() error = %v", err)
	}
	if pos != (lspdriver.Position{Line: 10, Column: 1}) {
		t.Fatalf("resolvePosition() = %+v, want {10 1}", pos)
	}
}

func TestResolvePosition_SingleSymbolOnLineUsedEvenWithoutName(t *testing.T) {
	driver := &fakeDriver{documentSyms: []lspdriver.Symbol{symAt("Foo", 10, 5)}}

	pos, err := resolvePosition(context.Background(), driver, "f.go", 10, "")
	if err != nil {
		t.Fatalf("resolvePosition() error = %v", err)
	}
	if pos.Column != 5 {
		t.Fatalf("Column = %d, want 5 (sole symbol on line)", pos.Column)
	}
}

func TestResolvePosition_MatchingSymbolNameSelectsItsColumn(t *testing.T) {
	driver := &fakeDriver{documentSyms: []lspdriver.Symbol{
		symAt("Foo", 10, 5),
		symAt("Bar", 10, 20),
	}}

	pos, err := resolvePosition(context.Background(), driver, "f.go", 10, "Bar")
	if err != nil {
		t.Fatalf("resolvePosition() error = %v", err)
	}
	if pos.Column != 20 {
		t.Fatalf("Column = %d, want 20 (matches symbol %q)", pos.Column, "Bar")
	}
}

func TestResolvePosition_UnmatchedSymbolNameFallsBackToColumnOne(t *testing.T) {
	driver := &fakeDriver{documentSyms: []lspdriver.Symbol{symAt("Foo", 10, 5)}}

	pos, err := resolvePosition(context.Background(), driver, "f.go", 10, "DoesNotExist")
	if err != nil {
		t.Fatalf("resolvePosition() error = %v", err)
	}
	if pos.Column != 1 {
		t.Fatalf("Column = %d, want 1 (no match, fall back)", pos.Column)
	}
}

func TestResolvePosition_MultipleSymbolsNoNameAmbiguousFallsBackToColumnOne(t *testing.T) {
	driver := &fakeDriver{documentSyms: []lspdriver.Symbol{
		symAt("Foo", 10, 5),
		symAt("Bar", 10, 20),
	}}

	pos, err := resolvePosition(context.Background(), driver, "f.go", 10, "")
	if err != nil {
		t.Fatalf("resolvePosition() error = %v", err)
	}
	if pos.Column != 1 {
		t.Fatalf("Column = %d, want 1 (ambiguous, no symbol given)", pos.Column)
	}
}
