package initdiscovery

import (
	"os"
	"path/filepath"
	"reflect"
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

// TestDetect_LSPCoverage_TypeScriptJavaScript_ProducesEnabledNote guards
// against the Language-to-LSP Table's multi-word display name
// ("TypeScript/JavaScript") breaking the Registry lookup, which is keyed by
// the single-word identifier "javascript".
func TestDetect_LSPCoverage_TypeScriptJavaScript_ProducesEnabledNote(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	withFakePATH(t, "typescript-language-server")
	writeFile(t, dir, "package.json", `{"name": "foo"}`)

	result := Detect(dir)

	found := false
	for _, n := range result.Notes {
		if n.Field == "lsp.enabled" && strings.Contains(n.Message, "TypeScript/JavaScript") {
			found = true
			if strings.Contains(n.Message, "Not found on PATH") {
				t.Errorf("typescript-language-server is on PATH, note should not claim it's missing: %q", n.Message)
			}
		}
		if n.Field == "lsp_no_server" && strings.Contains(n.Message, "TypeScript/JavaScript") {
			t.Errorf("TypeScript/JavaScript should be servable, got lsp_no_server note: %q", n.Message)
		}
	}
	if !found {
		t.Errorf("expected an lsp.enabled coverage Note mentioning TypeScript/JavaScript, got %+v", result.Notes)
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

func TestDetectLanguages_SingleLanguage_Manifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")

	languages := detectLanguages(dir)

	if len(languages) != 1 || languages[0] != "Go" {
		t.Errorf("detectLanguages = %v, want [Go]", languages)
	}
}

// TestDetectLanguages_MultiLanguage_Monorepo guards against issue #83: a
// hardcoded Go-only check that stops at the first match must not hide a
// second manifest-detected language in the same repository.
func TestDetectLanguages_MultiLanguage_Monorepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.25\n")
	writeFile(t, dir, "package.json", `{"name": "foo"}`)

	languages := detectLanguages(dir)

	want := []string{"Go", "TypeScript/JavaScript"}
	if !reflect.DeepEqual(languages, want) {
		t.Errorf("detectLanguages = %v, want %v", languages, want)
	}
}

func TestDetectLanguages_ManifestOnly_ExcludesUnrelatedLanguage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"name": "foo"}`)

	languages := detectLanguages(dir)

	want := []string{"TypeScript/JavaScript"}
	if !reflect.DeepEqual(languages, want) {
		t.Errorf("detectLanguages = %v, want %v", languages, want)
	}
	for _, l := range languages {
		if l == "Go" {
			t.Errorf("detectLanguages = %v, should not include Go without go.mod", languages)
		}
	}
}

func TestDetectLanguages_NoManifest_FallsBackToExtensions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.py", "print('hi')\n")

	languages := detectLanguages(dir)

	want := []string{"Python"}
	if !reflect.DeepEqual(languages, want) {
		t.Errorf("detectLanguages = %v, want %v", languages, want)
	}
}

func TestDetectLanguages_NoManifestNoSourceFiles_EmptyResult(t *testing.T) {
	dir := t.TempDir()

	languages := detectLanguages(dir)

	if len(languages) != 0 {
		t.Errorf("detectLanguages = %v, want empty", languages)
	}
}
