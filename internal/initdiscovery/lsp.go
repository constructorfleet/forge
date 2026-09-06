package initdiscovery

import (
	"fmt"
	"sort"
	"strings"

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

// detectLSPCoverage turns probe — the outcome of probing languages' ordered
// candidate Language Server binaries against PATH (see probeLanguageServers)
// — into Notes advertising that coverage: which languages have a binary
// ready now, and which have none on PATH yet. Every language in
// lsp.Languages has a Language Server Registry entry (see lsp.NewRegistry),
// so probe fully determines coverage; detectLSPCoverage never needs its own
// registry lookup or PATH probe.
func detectLSPCoverage(languages []string, probe LSPProbeResult) []Note {
	if len(languages) == 0 {
		return nil
	}

	var enabled []string
	for _, language := range languages {
		if _, ok := probe.Enabled[language]; ok {
			enabled = append(enabled, language)
		}
	}

	var notes []Note
	if len(enabled) > 0 {
		notes = append(notes, Note{
			Field:   "lsp.enabled",
			Message: fmt.Sprintf("Semantic navigation available for %s — set enabled: true.", strings.Join(enabled, ", ")),
		})
	}
	if len(probe.MissingBinaries) > 0 {
		var missing []string
		for _, m := range probe.MissingBinaries {
			missing = append(missing, fmt.Sprintf("%s (%s)", m.Language, m.Binary))
		}
		notes = append(notes, Note{
			Field:   "lsp_no_server",
			Message: fmt.Sprintf("Language Server binary not found on PATH for: %s.", strings.Join(missing, ", ")),
		})
	}

	return notes
}
