package toolsurface

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/semantic/gopls"
	"go.lsp.dev/protocol"
)

func fakeReadFile(content string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return []byte(content), nil }
}

func definitionCapableDriver() *fakeDriver {
	return &fakeDriver{
		caps: protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(true)},
		definition: []gopls.Location{
			{File: "f.go", Position: gopls.Position{Line: 5, Column: 3}},
		},
	}
}

func TestFindDefinition_InlinesFiveLineSnippet(t *testing.T) {
	src := strings.Join([]string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8"}, "\n") + "\n"
	ts := NewToolset(definitionCapableDriver(), Options{ReadFile: fakeReadFile(src)})

	got, err := ts.FindDefinition(context.Background(), "f.go", 5, "")
	if err != nil {
		t.Fatalf("FindDefinition() error = %v", err)
	}

	want := "l3\nl4\nl5\nl6\nl7"
	if got.Snippet != want {
		t.Fatalf("Snippet = %q, want %q", got.Snippet, want)
	}
	if got.File != "f.go" || got.Line != 5 || got.Col != 3 {
		t.Fatalf("location = %+v, want file f.go line 5 col 3", got)
	}
}

func TestFindDefinition_SnippetClampedNearFileStart(t *testing.T) {
	src := strings.Join([]string{"l1", "l2", "l3", "l4"}, "\n") + "\n"
	driver := &fakeDriver{
		caps:       protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(true)},
		definition: []gopls.Location{{File: "f.go", Position: gopls.Position{Line: 1, Column: 1}}},
	}
	ts := NewToolset(driver, Options{ReadFile: fakeReadFile(src)})

	got, err := ts.FindDefinition(context.Background(), "f.go", 1, "")
	if err != nil {
		t.Fatalf("FindDefinition() error = %v", err)
	}

	want := "l1\nl2\nl3"
	if got.Snippet != want {
		t.Fatalf("Snippet = %q, want %q", got.Snippet, want)
	}
}

func TestFindDefinition_NoResultsReturnsZeroValue(t *testing.T) {
	driver := &fakeDriver{caps: protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(true)}}
	ts := NewToolset(driver, Options{ReadFile: fakeReadFile("")})

	got, err := ts.FindDefinition(context.Background(), "f.go", 1, "")
	if err != nil {
		t.Fatalf("FindDefinition() error = %v", err)
	}
	if got != (SourceLocation{}) {
		t.Fatalf("FindDefinition() = %+v, want zero value", got)
	}
}

func TestFindDefinition_DriverErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	driver := &fakeDriver{caps: protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(true)}, err: wantErr}
	ts := NewToolset(driver, Options{ReadFile: fakeReadFile("")})

	_, err := ts.FindDefinition(context.Background(), "f.go", 1, "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("FindDefinition() error = %v, want %v", err, wantErr)
	}
}
