package lsp_test

import (
	"path/filepath"
	"testing"

	"github.com/Teagan42/forge/internal/lsp"
)

// languageSpecs returns a small, two-language table equivalent in shape to
// lsp.Languages, keyed by RegistryID (as goManifests/goExtensions key by
// RegistryID too), for exercising ScanLanguages without depending on the
// full production table.
func languageSpecs() []lsp.LanguageSpec {
	return []lsp.LanguageSpec{
		{Language: "go", ManifestFilenames: []string{"go.mod"}, FallbackExtensions: []string{".go"}},
		{Language: "javascript", ManifestFilenames: []string{"package.json"}, FallbackExtensions: []string{".js"}},
	}
}

// TestScanLanguages_FindsManifestDirectlyFromLanguageSpec checks that
// ScanLanguages derives its manifest patterns straight from each
// LanguageSpec's ManifestFilenames, with no caller-built ManifestPattern
// slice.
func TestScanLanguages_FindsManifestDirectlyFromLanguageSpec(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "sub", "go.mod"))

	result, err := lsp.ScanLanguages(root, languageSpecs())
	if err != nil {
		t.Fatalf("ScanLanguages: %v", err)
	}

	if !result.Manifests["go"] {
		t.Error(`result.Manifests["go"] = false, want true (manifest is in a subpackage)`)
	}
}

// TestScanLanguages_FallsBackToExtensionCountFromLanguageSpec checks that
// ScanLanguages derives its fallback extensions straight from each
// LanguageSpec's FallbackExtensions when no manifest is present.
func TestScanLanguages_FallsBackToExtensionCountFromLanguageSpec(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"))
	writeFile(t, filepath.Join(root, "pkg", "helper.go"))

	result, err := lsp.ScanLanguages(root, languageSpecs())
	if err != nil {
		t.Fatalf("ScanLanguages: %v", err)
	}

	if result.Manifests["go"] {
		t.Error(`result.Manifests["go"] = true, want false (no go.mod present)`)
	}
	if got := result.ExtensionCounts["go"]; got != 2 {
		t.Errorf(`result.ExtensionCounts["go"] = %d, want 2`, got)
	}
}
