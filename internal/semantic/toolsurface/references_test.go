package toolsurface

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func TestFindReferences_IncludeDeclarationTrueKeepsAll(t *testing.T) {
	driver := &fakeDriver{
		definition: []lspdriver.Location{{File: "f.go", Position: lspdriver.Position{Line: 1, Column: 1}}},
		references: []lspdriver.Location{
			{File: "f.go", Position: lspdriver.Position{Line: 1, Column: 1}},
			{File: "f.go", Position: lspdriver.Position{Line: 9, Column: 2}},
		},
	}
	ts := NewToolset(driver, Options{})

	got, err := ts.FindReferences(context.Background(), "f.go", 1, "", true)
	if err != nil {
		t.Fatalf("FindReferences() error = %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("Total = %d, want 2", got.Total)
	}
}

func TestFindReferences_IncludeDeclarationFalseExcludesDefinition(t *testing.T) {
	driver := &fakeDriver{
		definition: []lspdriver.Location{{File: "f.go", Position: lspdriver.Position{Line: 1, Column: 1}}},
		references: []lspdriver.Location{
			{File: "f.go", Position: lspdriver.Position{Line: 1, Column: 1}},
			{File: "f.go", Position: lspdriver.Position{Line: 9, Column: 2}},
		},
	}
	ts := NewToolset(driver, Options{})

	got, err := ts.FindReferences(context.Background(), "f.go", 1, "", false)
	if err != nil {
		t.Fatalf("FindReferences() error = %v", err)
	}
	if got.Total != 1 {
		t.Fatalf("Total = %d, want 1 (declaration excluded)", got.Total)
	}
	if got.Items[0].Line != 9 {
		t.Fatalf("Items[0].Line = %d, want 9", got.Items[0].Line)
	}
}

func TestFindReferences_CapsAtMaxResults(t *testing.T) {
	refs := make([]lspdriver.Location, 60)
	for i := range refs {
		refs[i] = lspdriver.Location{File: "f.go", Position: lspdriver.Position{Line: i + 1, Column: 1}}
	}
	driver := &fakeDriver{references: refs}
	ts := NewToolset(driver, Options{MaxResults: 50})

	got, err := ts.FindReferences(context.Background(), "f.go", 1, "", true)
	if err != nil {
		t.Fatalf("FindReferences() error = %v", err)
	}
	if !got.Truncated || got.Total != 60 || len(got.Items) != 50 {
		t.Fatalf("got = %+v, want truncated 50-of-60", got)
	}
}

func TestFindImplementations_LocationOnlyAndCapped(t *testing.T) {
	impls := make([]lspdriver.Location, 3)
	for i := range impls {
		impls[i] = lspdriver.Location{File: "f.go", Position: lspdriver.Position{Line: i + 1, Column: 1}}
	}
	driver := &fakeDriver{implementation: impls}
	ts := NewToolset(driver, Options{})

	got, err := ts.FindImplementations(context.Background(), "f.go", 1, "")
	if err != nil {
		t.Fatalf("FindImplementations() error = %v", err)
	}
	if got.Total != 3 || got.Truncated {
		t.Fatalf("got = %+v, want 3 untruncated", got)
	}
}
