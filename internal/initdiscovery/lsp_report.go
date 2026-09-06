package initdiscovery

import (
	"fmt"
	"sort"
)

// FormatMissingBinariesReport renders forge init's consolidated batch report
// of languages whose candidate Language Server binaries are all absent from
// PATH: one line per entry in missing, naming the preferred candidate
// binary and its static install hint. It never prompts the user; it only
// formats text for the caller to print after the probe pass completes.
func FormatMissingBinariesReport(missing []MissingBinary) []string {
	lines := make([]string, 0, len(missing))
	for _, m := range missing {
		lines = append(lines, fmt.Sprintf("%s: %s not found on PATH. Install it: %s", m.Language, m.Binary, m.InstallHint))
	}
	return lines
}

// FormatEnabledLSPsReport renders forge init's end-of-run summary of every
// enabled Language Server: one line per detected language with a candidate
// binary found on PATH, naming the language and the binary that was
// enabled. Lines sort by language name so the report is deterministic
// regardless of map iteration order.
func FormatEnabledLSPsReport(enabled map[string]string) []string {
	languages := make([]string, 0, len(enabled))
	for language := range enabled {
		languages = append(languages, language)
	}
	sort.Strings(languages)

	lines := make([]string, 0, len(languages))
	for _, language := range languages {
		lines = append(lines, fmt.Sprintf("%s: %s", language, enabled[language]))
	}
	return lines
}
