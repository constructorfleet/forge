package toolsurface

import (
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func TestSourceLocationFromLSPLocation(t *testing.T) {
	loc := lspdriver.Location{File: "/repo/main.go", Position: lspdriver.Position{Line: 12, Column: 4}}

	got := sourceLocationFromLSPLocation(loc)

	want := SourceLocation{File: "/repo/main.go", Line: 12, Col: 4}
	if got != want {
		t.Fatalf("sourceLocationFromLSPLocation() = %+v, want %+v", got, want)
	}
}

func TestSourceLocationFromLSPSymbol(t *testing.T) {
	sym := lspdriver.Symbol{
		Name: "Foo",
		Kind: "function",
		Location: lspdriver.Location{
			File:     "/repo/foo.go",
			Position: lspdriver.Position{Line: 3, Column: 1},
		},
	}

	got := sourceLocationFromLSPSymbol(sym)

	want := SourceLocation{File: "/repo/foo.go", Line: 3, Col: 1, SymbolName: "Foo", Kind: "function"}
	if got != want {
		t.Fatalf("sourceLocationFromLSPSymbol() = %+v, want %+v", got, want)
	}
}
