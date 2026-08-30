package toolsurface

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func TestSymbolInfo_MapsSignatureTypeDocsAndDefinition(t *testing.T) {
	def := lspdriver.Location{File: "f.go", Position: lspdriver.Position{Line: 4, Column: 2}}
	driver := &fakeDriver{
		symbolInfo: lspdriver.SymbolInfo{
			Signature:     "func Foo() error",
			Documentation: "Foo does a thing.",
			Definition:    &def,
		},
	}
	ts := NewToolset(driver, Options{})

	got, err := ts.SymbolInfo(context.Background(), "f.go", 4)
	if err != nil {
		t.Fatalf("SymbolInfo() error = %v", err)
	}

	if got.Signature != "func Foo() error" {
		t.Fatalf("Signature = %q", got.Signature)
	}
	if got.Type != "func Foo() error" {
		t.Fatalf("Type = %q, want same as Signature (gopls hover doesn't split them)", got.Type)
	}
	if got.Docs != "Foo does a thing." {
		t.Fatalf("Docs = %q", got.Docs)
	}
	if got.DefinitionLocation == nil || got.DefinitionLocation.Line != 4 || got.DefinitionLocation.Col != 2 {
		t.Fatalf("DefinitionLocation = %+v", got.DefinitionLocation)
	}
}

func TestSymbolInfo_NoDefinitionLeavesLocationNil(t *testing.T) {
	driver := &fakeDriver{symbolInfo: lspdriver.SymbolInfo{Signature: "x"}}
	ts := NewToolset(driver, Options{})

	got, err := ts.SymbolInfo(context.Background(), "f.go", 1)
	if err != nil {
		t.Fatalf("SymbolInfo() error = %v", err)
	}
	if got.DefinitionLocation != nil {
		t.Fatalf("DefinitionLocation = %+v, want nil", got.DefinitionLocation)
	}
}
