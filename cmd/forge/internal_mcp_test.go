package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
	"github.com/Teagan42/forge/internal/semantic/lspdriver"
)

func TestRunInternalMCP_MissingWorkspaceFlag(t *testing.T) {
	code := runInternalMCP(nil)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error)", code)
	}
}

func TestRunInternalMCP_UnknownFlag(t *testing.T) {
	code := runInternalMCP([]string{"--bogus"})
	if code != 2 {
		t.Fatalf("code = %d, want 2 (flag parse error)", code)
	}
}

func TestRunInternalMCP_NonexistentWorkspaceDir(t *testing.T) {
	code := runInternalMCP([]string{"--workspace", "/nonexistent/path/for/forge/internal-mcp/test"})
	if code != 1 {
		t.Fatalf("code = %d, want 1 (workspace does not exist)", code)
	}
}

// TestRunInternalMCP_NoDetectedLanguages fails fast rather than serving an
// empty tool set: a workspace with no manifest Forge recognizes has no
// language server to multiplex, so there is nothing for this subcommand to
// serve.
func TestRunInternalMCP_NoDetectedLanguages(t *testing.T) {
	code := runInternalMCP([]string{"--workspace", t.TempDir()})
	if code != 1 {
		t.Fatalf("code = %d, want 1 (no language detected in an empty workspace)", code)
	}
}

// TestDetectWorkspaceServers_MixedWorkspace is acceptance criterion 1's
// detection half: a workspace carrying all four manifests yields one
// Detected Server per language, each with its registry ServerProfile.
func TestDetectWorkspaceServers_MixedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	for _, manifest := range []string{"go.mod", "Cargo.toml", "pyproject.toml", "package.json"} {
		if err := os.WriteFile(filepath.Join(workspace, manifest), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", manifest, err)
		}
	}

	got, err := detectWorkspaceServers(config.Default(), workspace)
	if err != nil {
		t.Fatalf("detectWorkspaceServers() error = %v", err)
	}

	byLanguage := make(map[string]lsp.DetectedServer, len(got))
	for _, s := range got {
		byLanguage[s.Language] = s
	}
	for _, language := range []string{"go", "rust", "python", "javascript"} {
		if _, ok := byLanguage[language]; !ok {
			t.Errorf("detectWorkspaceServers() = %+v, want a %q server", got, language)
		}
	}
	if !byLanguage["python"].Profile.DropSymbolChildren {
		t.Errorf("python server Profile = %+v, want DropSymbolChildren (pyright's registry profile)", byLanguage["python"].Profile)
	}
	if byLanguage["rust"].Profile.HoverStyle != lspdriver.HoverStyleRustTwoFence {
		t.Errorf("rust server Profile = %+v, want HoverStyleRustTwoFence", byLanguage["rust"].Profile)
	}
}

// TestDriverOptions_CarriesRegistryProfile pins the fix for a driver built
// without its server's ServerProfile: harmless while only gopls ran (its
// profile is the zero value), wrong for every other language.
func TestDriverOptions_CarriesRegistryProfile(t *testing.T) {
	cfg := config.LSPConfig{ReadinessTimeout: 7 * time.Second, RestartLimit: 3}
	server := lsp.DetectedServer{
		Language: "python",
		Command:  []string{"pyright-langserver", "--stdio"},
		Profile: lspdriver.ServerProfile{
			HoverStyle:         lspdriver.HoverStylePyrightAnnotated,
			DropSymbolChildren: true,
		},
	}

	got := driverOptions("/workspace", cfg, server)

	want := lspdriver.Options{
		Command:          []string{"pyright-langserver", "--stdio"},
		Dir:              "/workspace",
		ReadinessTimeout: 7 * time.Second,
		RestartLimit:     3,
		Profile:          server.Profile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("driverOptions() = %+v, want %+v", got, want)
	}
}

// TestStartDrivers_OneDriverPerDetectedLanguage is acceptance criterion 1's
// startup half. The commands intentionally do not exist: lspdriver.Driver
// degrades a server it cannot launch to inert rather than failing, so this
// asserts the multiplexer's structure — one driver keyed per language —
// without needing four real language servers installed.
func TestStartDrivers_OneDriverPerDetectedLanguage(t *testing.T) {
	cfg := config.LSPConfig{ReadinessTimeout: 100 * time.Millisecond}
	servers := []lsp.DetectedServer{
		{Language: "go", Command: []string{"forge-test-no-such-server"}},
		{Language: "rust", Command: []string{"forge-test-no-such-server"}},
		{Language: "python", Command: []string{"forge-test-no-such-server"}},
		{Language: "javascript", Command: []string{"forge-test-no-such-server"}},
	}

	drivers, shutdown := startDrivers(context.Background(), t.TempDir(), cfg, servers)
	defer shutdown()

	if len(drivers) != 4 {
		t.Fatalf("startDrivers() = %v drivers, want one per detected language", len(drivers))
	}
	for _, language := range []string{"go", "rust", "python", "javascript"} {
		if drivers[language] == nil {
			t.Errorf("startDrivers() has no driver for %q", language)
		}
	}
}
