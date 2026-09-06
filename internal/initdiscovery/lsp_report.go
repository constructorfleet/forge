package initdiscovery

import "fmt"

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
