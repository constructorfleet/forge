package initdiscovery

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
)

// detectLanguages returns the sorted, deduplicated set of languages detected
// at dir: walking the tree for every language in the Language-to-LSP Table
// (lsp.Languages), independently, so a repository with manifests for more
// than one language (a monorepo) reports every one of them rather than
// stopping at the first match.
func detectLanguages(dir string) []string {
	manifests := make([]lsp.ManifestPattern, 0, len(lsp.Languages))
	extensions := make([]lsp.ExtensionSpec, 0, len(lsp.Languages))
	for _, spec := range lsp.Languages {
		manifests = append(manifests, lsp.ManifestPattern{Language: spec.Language, Filenames: spec.ManifestFilenames})
		extensions = append(extensions, lsp.ExtensionSpec{Language: spec.Language, Extensions: spec.FallbackExtensions})
	}

	result, err := lsp.Scan(dir, manifests, extensions)
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	for language, present := range result.Manifests {
		if present {
			seen[language] = true
		}
	}
	for language, count := range result.ExtensionCounts {
		if count > 0 {
			seen[language] = true
		}
	}

	languages := make([]string, 0, len(seen))
	for language := range seen {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// registryKeys maps a Language-to-LSP Table display name (lsp.Languages,
// e.g. "TypeScript/JavaScript") to the Language Server Registry's
// single-word, lowercase identifier (lsp.Registry, e.g. "javascript"). The
// two tables are not yet reconciled (see the caveat on lsp.Languages), so
// this table is how detectLSPCoverage bridges a display name to the key
// lsp.Detect and the registry actually use.
var registryKeys = map[string]string{
	"Go":                    "go",
	"TypeScript/JavaScript": "javascript",
	"Python":                "python",
	"Rust":                  "rust",
}

// registryKey returns the Language Server Registry key for a Language-to-LSP
// Table display name, falling back to a plain lowercase of the name for a
// language the registry table above does not list (no registry entry can
// match it either way).
func registryKey(language string) string {
	if key, ok := registryKeys[language]; ok {
		return key
	}
	return strings.ToLower(language)
}

// detectLSPCoverage checks which of the detected languages Forge can serve
// via its Language Server Registry (internal/lsp) and returns Notes
// advertising that coverage — never config values. lsp.servers stays empty;
// the registry, not .forge.yaml, is the source of server commands (see
// lsp.NewRegistry).
func detectLSPCoverage(languages []string, cfg config.LSPConfig) []Note {
	if len(languages) == 0 {
		return nil
	}

	registryLanguages := make([]string, len(languages))
	for i, language := range languages {
		registryLanguages[i] = registryKey(language)
	}

	registry := lsp.NewRegistry(cfg)
	servers := lsp.Detect(registryLanguages, registry)

	servable := make(map[string]bool, len(servers))
	var missingOnPATH []string
	for _, server := range servers {
		servable[server.Language] = true
		if _, err := exec.LookPath(server.Command[0]); err != nil {
			missingOnPATH = append(missingOnPATH, server.Command[0])
		}
	}

	var servableLanguages, unservableLanguages []string
	for _, language := range languages {
		if servable[registryKey(language)] {
			servableLanguages = append(servableLanguages, language)
		} else {
			unservableLanguages = append(unservableLanguages, language)
		}
	}
	sort.Strings(missingOnPATH)

	var notes []Note
	if len(servableLanguages) > 0 {
		msg := fmt.Sprintf("Semantic navigation available for %s — set enabled: true.", strings.Join(servableLanguages, ", "))
		if len(missingOnPATH) > 0 {
			msg += fmt.Sprintf(" Not found on PATH: %s.", strings.Join(missingOnPATH, ", "))
		}
		notes = append(notes, Note{Field: "lsp.enabled", Message: msg})
	}
	if len(unservableLanguages) > 0 {
		notes = append(notes, Note{
			Field:   "lsp_no_server",
			Message: fmt.Sprintf("no Language Server available yet for: %s.", strings.Join(unservableLanguages, ", ")),
		})
	}

	return notes
}
