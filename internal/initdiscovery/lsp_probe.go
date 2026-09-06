package initdiscovery

import "github.com/Teagan42/forge/internal/lsp"

// LSPProbeResult is the outcome of probing each detected language's
// ordered candidate Language Server binaries (lsp.Languages) against PATH:
// which languages got a binary enabled, and which have none available.
type LSPProbeResult struct {
	// Enabled maps a detected language (lsp.Languages display name) to the
	// first candidate binary exec.LookPath resolved for it.
	Enabled map[string]string
	// Missing lists detected languages for which no candidate binary
	// resolved on PATH.
	Missing []string
}

// languageSpecs indexes lsp.Languages by display name for lookup by the
// names detectLanguages returns.
var languageSpecs = func() map[string]lsp.LanguageSpec {
	specs := make(map[string]lsp.LanguageSpec, len(lsp.Languages))
	for _, spec := range lsp.Languages {
		specs[spec.Language] = spec
	}
	return specs
}()

// probeLanguageServers probes languages (as returned by detectLanguages)
// against the Language-to-LSP Table and records, per language, the first
// candidate binary found on PATH (lsp.ProbeBinaries) or marks it missing
// when none resolve. It never prompts the user or installs anything. A
// language absent from lsp.Languages is silently skipped: it has no
// candidate binaries to probe.
func probeLanguageServers(languages []string) LSPProbeResult {
	result := LSPProbeResult{Enabled: map[string]string{}}
	for _, language := range languages {
		spec, ok := languageSpecs[language]
		if !ok {
			continue
		}
		if binary, found := lsp.ProbeBinaries(spec); found {
			result.Enabled[language] = binary
		} else {
			result.Missing = append(result.Missing, language)
		}
	}
	return result
}
