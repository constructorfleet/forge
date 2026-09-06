package lsp

import (
	"io/fs"
	"path/filepath"
)

// excludedDirs holds directory names the walker never descends into.
var excludedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"out":          true,
	"target":       true,
	"bin":          true,
	"obj":          true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".next":        true,
	".turbo":       true,
}

// ManifestPattern names the manifest filenames that mark a language's
// presence, for example go.mod for Go.
type ManifestPattern struct {
	Language  string
	Filenames []string
}

// ExtensionSpec names the fallback source-file extensions for a language,
// used only when no manifest for that language is found.
type ExtensionSpec struct {
	Language   string
	Extensions []string
}

// ScanResult is the outcome of walking a repository tree: for each
// language, whether a manifest was found, and, for languages with no
// manifest, the summed count of fallback-extension files.
type ScanResult struct {
	Manifests       map[string]bool
	ExtensionCounts map[string]int
}

// Scan walks root recursively, pruning directories in excludedDirs, and
// finds each language's manifest anywhere in the pruned tree. For a
// language with no manifest, it sums matching fallback extensions across
// the whole pruned tree. Scan invokes no external tool; it uses only
// filesystem stat/readdir and filename pattern matching.
func Scan(root string, manifests []ManifestPattern, extensions []ExtensionSpec) (ScanResult, error) {
	manifestLanguage := make(map[string]string)
	for _, m := range manifests {
		for _, filename := range m.Filenames {
			manifestLanguage[filename] = m.Language
		}
	}
	extensionLanguage := make(map[string]string)
	for _, e := range extensions {
		for _, ext := range e.Extensions {
			extensionLanguage[ext] = e.Language
		}
	}

	found := make(map[string]bool)
	counts := make(map[string]int)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && excludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if language, ok := manifestLanguage[entry.Name()]; ok {
			found[language] = true
		}
		if language, ok := extensionLanguage[filepath.Ext(entry.Name())]; ok {
			counts[language]++
		}
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}

	result := ScanResult{
		Manifests:       make(map[string]bool, len(manifests)),
		ExtensionCounts: make(map[string]int),
	}
	for _, m := range manifests {
		result.Manifests[m.Language] = found[m.Language]
	}
	for _, e := range extensions {
		if !result.Manifests[e.Language] {
			result.ExtensionCounts[e.Language] = counts[e.Language]
		}
	}
	return result, nil
}

// ScanLanguages walks root exactly as Scan does, but reads its manifest
// filenames and fallback extensions straight from languages (the
// Language-to-LSP Table's LanguageSpec rows), so a caller never builds its
// own ManifestPattern and ExtensionSpec slices by hand.
func ScanLanguages(root string, languages []LanguageSpec) (ScanResult, error) {
	manifests := make([]ManifestPattern, 0, len(languages))
	extensions := make([]ExtensionSpec, 0, len(languages))
	for _, spec := range languages {
		manifests = append(manifests, ManifestPattern{Language: spec.Language, Filenames: spec.ManifestFilenames})
		extensions = append(extensions, ExtensionSpec{Language: spec.Language, Extensions: spec.FallbackExtensions})
	}
	return Scan(root, manifests, extensions)
}
