package lsp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Teagan42/forge/internal/lsp"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func goManifests() []lsp.ManifestPattern {
	return []lsp.ManifestPattern{
		{Language: "go", Filenames: []string{"go.mod"}},
		{Language: "javascript", Filenames: []string{"package.json"}},
	}
}

func goExtensions() []lsp.ExtensionSpec {
	return []lsp.ExtensionSpec{
		{Language: "go", Extensions: []string{".go"}},
		{Language: "javascript", Extensions: []string{".js"}},
	}
}

func TestScan_FindsManifestInSubpackageWithNoRootManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "sub", "go.mod"))

	result, err := lsp.Scan(root, goManifests(), goExtensions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if !result.Manifests["go"] {
		t.Error(`result.Manifests["go"] = false, want true (manifest is in a subpackage)`)
	}
}

func TestScan_IgnoresDecoyManifestInExcludedDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "node_modules", "some-pkg", "package.json"))

	result, err := lsp.Scan(root, goManifests(), goExtensions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if result.Manifests["javascript"] {
		t.Error(`result.Manifests["javascript"] = true, want false (decoy manifest lives under an excluded dir)`)
	}
}

func TestScan_FallsBackToExtensionCountWhenNoManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"))
	writeFile(t, filepath.Join(root, "pkg", "helper.go"))

	result, err := lsp.Scan(root, goManifests(), goExtensions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if result.Manifests["go"] {
		t.Error(`result.Manifests["go"] = true, want false (no go.mod present)`)
	}
	if got := result.ExtensionCounts["go"]; got != 2 {
		t.Errorf(`result.ExtensionCounts["go"] = %d, want 2`, got)
	}
}

func TestScan_ManifestFoundSuppressesExtensionCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"))
	writeFile(t, filepath.Join(root, "main.go"))

	result, err := lsp.Scan(root, goManifests(), goExtensions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if !result.Manifests["go"] {
		t.Error(`result.Manifests["go"] = false, want true`)
	}
	if _, ok := result.ExtensionCounts["go"]; ok {
		t.Error(`result.ExtensionCounts["go"] present, want absent once a manifest is found`)
	}
}

func TestScan_AggregatesExtensionCountsAsUnionAcrossWholeTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.js"))
	writeFile(t, filepath.Join(root, "sub1", "b.js"))
	writeFile(t, filepath.Join(root, "sub2", "deeper", "c.js"))

	result, err := lsp.Scan(root, goManifests(), goExtensions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got := result.ExtensionCounts["javascript"]; got != 3 {
		t.Errorf(`result.ExtensionCounts["javascript"] = %d, want 3 (summed across whole pruned tree)`, got)
	}
}

func TestScan_PrunesAllExcludedDirectoryNames(t *testing.T) {
	root := t.TempDir()
	excluded := []string{
		".git", "node_modules", "vendor", "dist", "build", "out",
		"target", "bin", "obj", ".venv", "venv", "__pycache__", ".next", ".turbo",
	}
	for _, dir := range excluded {
		writeFile(t, filepath.Join(root, dir, "decoy.go"))
	}
	writeFile(t, filepath.Join(root, "real.go"))

	result, err := lsp.Scan(root, goManifests(), goExtensions())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got := result.ExtensionCounts["go"]; got != 1 {
		t.Errorf(`result.ExtensionCounts["go"] = %d, want 1 (only the non-excluded file counts)`, got)
	}
}
