package tracker

import "strings"

// ApplyOverrides applies `.forge.yaml` Dependency overrides to the
// Dependencies parsed from an issue body, per CONTEXT.md "Dependency
// Source": config overrides take precedence over the canonical `##
// Dependencies` block when present for the given issue ID. An override
// entry present in the map — including an explicit empty list — fully
// replaces the parsed list; issues absent from the overrides map keep
// their parsed Dependencies unchanged.
//
// Override keys are matched after stripping an optional leading '#' and
// surrounding whitespace, since a `.forge.yaml` author may naturally write
// an issue reference as "#42" (as it appears in `## Dependencies` syntax)
// even though issueID is always the bare numeric form internally.
func ApplyOverrides(issueID string, parsed []string, overrides map[string][]string) []string {
	for key, override := range overrides {
		if normalizeIssueRef(key) == issueID {
			return override
		}
	}
	return parsed
}

// normalizeIssueRef strips an optional leading '#' and surrounding
// whitespace from an issue reference.
func normalizeIssueRef(ref string) string {
	return strings.TrimPrefix(strings.TrimSpace(ref), "#")
}
