package initdiscovery

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
)

// languageManifest associates a project manifest file with the language it
// indicates, mirroring repocontext's manifest table so forge init's LSP
// coverage notes name languages consistently with the Repository Context
// Workers see. Detection here is only ever used to check registry
// coverage — it never populates lsp.servers (see NewRegistry: the registry,
// not config, is the source of server commands).
var languageManifests = []struct {
	file     string
	language string
}{
	{"go.mod", "Go"},
	{"Cargo.toml", "Rust"},
	{"Gemfile", "Ruby"},
	{"pom.xml", "Java"},
	{"build.gradle", "Java"},
	{"build.gradle.kts", "Kotlin"},
	{"requirements.txt", "Python"},
	{"pyproject.toml", "Python"},
}

// detectLanguages returns the sorted, deduplicated set of languages whose
// manifest files are present at dir.
func detectLanguages(dir string) []string {
	seen := map[string]bool{}
	for _, m := range languageManifests {
		if fileExists(filepath.Join(dir, m.file)) {
			seen[m.language] = true
		}
	}
	if fileExists(filepath.Join(dir, "package.json")) {
		seen["JavaScript"] = true
	}

	languages := make([]string, 0, len(seen))
	for language := range seen {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// detectLSPCoverage checks which of the detected languages Forge can serve
// via its Language Server Registry (internal/lsp) and returns Notes
// advertising that coverage — never config values. lsp.servers stays empty;
// the registry, not .forge.yaml, is the source of server commands (see
// lsp.NewRegistry).
func detectLSPCoverage(dir string, cfg config.LSPConfig) []Note {
	languages := detectLanguages(dir)
	if len(languages) == 0 {
		return nil
	}

	registry := lsp.NewRegistry(cfg)
	servers := lsp.Detect(languages, registry)

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
		if servable[strings.ToLower(language)] {
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
