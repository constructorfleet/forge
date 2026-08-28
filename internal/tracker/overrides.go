package tracker

// ApplyOverrides applies `.forge.yaml` Dependency overrides to the
// Dependencies parsed from an issue body, per CONTEXT.md "Dependency
// Source": config overrides take precedence over the canonical `##
// Dependencies` block when present for the given issue ID. An override
// entry present in the map — including an explicit empty list — fully
// replaces the parsed list; issues absent from the overrides map keep
// their parsed Dependencies unchanged.
func ApplyOverrides(issueID string, parsed []string, overrides map[string][]string) []string {
	if override, ok := overrides[issueID]; ok {
		return override
	}
	return parsed
}
