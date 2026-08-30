package toolsurface

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func TestSearchSymbols_FileScopeUsesDocumentSymbols(t *testing.T) {
	driver := &fakeDriver{documentSyms: []lspdriver.Symbol{symAt("Foo", 1, 1), symAt("Bar", 2, 1)}}
	ts := NewToolset(driver, Options{})

	got, err := ts.SearchSymbols(context.Background(), "Foo", ScopeFile, "f.go")
	if err != nil {
		t.Fatalf("SearchSymbols() error = %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("Total = %d, want 2 (document symbols aren't filtered by this method)", got.Total)
	}
	if got.Items[0].SymbolName != "Foo" {
		t.Fatalf("Items[0].SymbolName = %q", got.Items[0].SymbolName)
	}
}

func TestSearchSymbols_WorkspaceScopeUsesWorkspaceSymbols(t *testing.T) {
	driver := &fakeDriver{workspaceSyms: []lspdriver.Symbol{symAt("Baz", 5, 1)}}
	ts := NewToolset(driver, Options{})

	got, err := ts.SearchSymbols(context.Background(), "Baz", ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("SearchSymbols() error = %v", err)
	}
	if got.Total != 1 || got.Items[0].SymbolName != "Baz" {
		t.Fatalf("got = %+v", got)
	}
}

func TestSearchSymbols_FileScopeWithoutFileErrors(t *testing.T) {
	ts := NewToolset(&fakeDriver{}, Options{})

	_, err := ts.SearchSymbols(context.Background(), "Foo", ScopeFile, "")
	if err == nil {
		t.Fatalf("SearchSymbols() error = nil, want error (file scope requires file)")
	}
}

func TestSearchSymbols_UnknownScopeErrors(t *testing.T) {
	ts := NewToolset(&fakeDriver{}, Options{})

	_, err := ts.SearchSymbols(context.Background(), "Foo", Scope("bogus"), "f.go")
	if err == nil {
		t.Fatalf("SearchSymbols() error = nil, want error for unknown scope")
	}
}

func TestSearchSymbols_CapsAtMaxResults(t *testing.T) {
	syms := make([]lspdriver.Symbol, 60)
	for i := range syms {
		syms[i] = symAt("S", i+1, 1)
	}
	driver := &fakeDriver{workspaceSyms: syms}
	ts := NewToolset(driver, Options{MaxResults: 50})

	got, err := ts.SearchSymbols(context.Background(), "S", ScopeWorkspace, "")
	if err != nil {
		t.Fatalf("SearchSymbols() error = %v", err)
	}
	if !got.Truncated || got.Total != 60 || len(got.Items) != 50 {
		t.Fatalf("got = %+v", got)
	}
}

func TestSearchSymbols_DriverErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	driver := &fakeDriver{err: wantErr}
	ts := NewToolset(driver, Options{})

	_, err := ts.SearchSymbols(context.Background(), "Foo", ScopeWorkspace, "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("SearchSymbols() error = %v, want %v", err, wantErr)
	}
}
