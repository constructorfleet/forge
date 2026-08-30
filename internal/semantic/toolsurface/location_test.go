package toolsurface

import (
	"testing"

	"github.com/Teagan42/forge/internal/semantic/gopls"
)

func TestSourceLocationFromGoplsLocation(t *testing.T) {
	loc := gopls.Location{File: "/repo/main.go", Position: gopls.Position{Line: 12, Column: 4}}

	got := sourceLocationFromGoplsLocation(loc)

	want := SourceLocation{File: "/repo/main.go", Line: 12, Col: 4}
	if got != want {
		t.Fatalf("sourceLocationFromGoplsLocation() = %+v, want %+v", got, want)
	}
}

func TestSourceLocationFromGoplsSymbol(t *testing.T) {
	sym := gopls.Symbol{
		Name: "Foo",
		Kind: "function",
		Location: gopls.Location{
			File:     "/repo/foo.go",
			Position: gopls.Position{Line: 3, Column: 1},
		},
	}

	got := sourceLocationFromGoplsSymbol(sym)

	want := SourceLocation{File: "/repo/foo.go", Line: 3, Col: 1, SymbolName: "Foo", Kind: "function"}
	if got != want {
		t.Fatalf("sourceLocationFromGoplsSymbol() = %+v, want %+v", got, want)
	}
}
