package initdiscovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakePATH points PATH at a directory containing only the executables
// named in present, so exec.LookPath resolves those and only those —
// deterministic regardless of what's actually installed on the machine
// running the test.
func withFakePATH(t *testing.T, present ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range present {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestDetect_LSPCoverage_ServableLanguage_ProducesEnabledNote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	withFakePATH(t, "gopls")
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	result := Detect(dir)

	if result.Config.LSP.Enabled {
		t.Errorf("LSP.Enabled = true, want false (opt-in only)")
	}
	if len(result.Config.LSP.Servers) != 0 {
		t.Errorf("LSP.Servers = %+v, want empty (registry provides commands, no duplication)", result.Config.LSP.Servers)
	}

	found := false
	for _, n := range result.Notes {
		if n.Field == "lsp.enabled" && strings.Contains(n.Message, "Go") {
			found = true
			if strings.Contains(n.Message, "Not found on PATH") {
				t.Errorf("gopls is on PATH, note should not claim it's missing: %q", n.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected an lsp.enabled coverage Note mentioning Go, got %+v", result.Notes)
	}

	mustLoadable(t, result)
}

func TestDetect_LSPCoverage_MissingServerBinary_ProducesPathProbeNote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	withFakePATH(t) // empty PATH: gopls is not resolvable
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	result := Detect(dir)

	found := false
	for _, n := range result.Notes {
		if n.Field == "lsp.enabled" && strings.Contains(n.Message, "gopls") && strings.Contains(n.Message, "Not found on PATH") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an lsp.enabled Note about gopls missing from PATH, got %+v", result.Notes)
	}

	mustLoadable(t, result)
}

func TestDetect_LSPCoverage_NonServableLanguage_ProducesHeaderNote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	withFakePATH(t)
	writeFile(t, dir, "pom.xml", "<project></project>\n")

	result := Detect(dir)

	found := false
	for _, n := range result.Notes {
		if n.Field == "lsp_no_server" && strings.Contains(n.Message, "Java") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an lsp_no_server Note mentioning Java, got %+v", result.Notes)
	}

	out, err := Render(result)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "Java") {
		t.Errorf("rendered output missing Java no-server note:\n%s", out)
	}

	mustLoadable(t, result)
}

func TestDetect_LSPCoverage_NoDetectedLanguages_NoNotes(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	result := Detect(dir)

	for _, n := range result.Notes {
		if n.Field == "lsp.enabled" || n.Field == "lsp_no_server" {
			t.Errorf("did not expect an LSP Note with no detected languages, got %+v", n)
		}
	}
	mustLoadable(t, result)
}
