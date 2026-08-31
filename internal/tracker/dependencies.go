// Package tracker defines Forge's normalized Tracker Adapter interface (see
// CONTEXT.md "Tracker Adapter"). Scheduler-facing code in this package and
// its callers contains no tracker-specific models — everything here is
// expressed in terms of domain types. Tracker-specific implementations
// (e.g. internal/tracker/github) live in subpackages and translate their
// native API shapes into these normalized types.
package tracker

import (
	"fmt"
	"regexp"
	"strings"
)

// The canonical Dependency Source is a `## Dependencies` block in the issue
// body (see CONTEXT.md "Dependency Source" and ADR 0003). Only the strict
// syntax below is accepted; freeform text is rejected rather than guessed
// at via NLP, so Dependency parsing stays deterministic.
var (
	reHeaderNone     = regexp.MustCompile(`(?i)^##\s+Dependencies:\s*None\s*$`)
	reHeader         = regexp.MustCompile(`(?i)^##\s+Dependencies\s*$`)
	reNearMissHeader = regexp.MustCompile(`(?i)^##\s+Dependencies\b`)
	reAnyHeading     = regexp.MustCompile(`^#`)
	reThematicBreak  = regexp.MustCompile(`^(?:-{3,}|\*{3,}|_{3,})$`)
	reLabel          = regexp.MustCompile(`(?i)^Depends on:\s*$`)
	reNoneLine       = regexp.MustCompile(`(?i)^none[.!]?$`)
	reInlineDepends  = regexp.MustCompile(`(?i)^Depends on:\s*(#\d+(?:\s*,\s*#\d+)*)\s*$`)
	// A bullet is a single explicit issue ref, optionally followed by a
	// human-readable annotation — either a parenthetical ("- #96 (SaveGoal
	// loader method)") or a separator-led description ("- #198 — Add the
	// contract field", "- #7: rationale"). The annotation is descriptive
	// text, not another dependency — the "#\d+" is still explicit, so
	// accepting it requires no NLP inference. Capture group 2 holds the
	// annotation so the caller can fail closed if it hides a second ref.
	reBullet   = regexp.MustCompile(`^-\s*#(\d+)\s*([(:—–-].*)?$`)
	reIssueRef = regexp.MustCompile(`#(\d+)`)
)

// ParseDependencyBlock extracts the list of prerequisite Issue IDs from the
// canonical `## Dependencies` block in an issue body. It recognizes:
//
//   - `## Dependencies: None` — no Dependencies (returns an empty slice)
//   - `## Dependencies` followed by a bare `None` (or `None.`) line — same
//     as the inline `None` form (returns an empty slice)
//   - `## Dependencies` followed by a bullet list (`- #123`, optionally with
//     a trailing annotation — a parenthetical like `- #123 (why)` or a
//     separator-led description like `- #123 — why` / `- #123: why`),
//     optionally preceded by a `Depends on:` label line
//   - `## Dependencies` followed by an inline `Depends on: #123, #456` line
//
// If the issue body contains no `## Dependencies` block at all, it returns
// an empty slice and no error — Dependencies are simply absent. Any other
// content inside the block (freeform text) is rejected with a descriptive
// error, since Forge does not do NLP-based dependency inference.
func ParseDependencyBlock(body string) ([]string, error) {
	lines := strings.Split(body, "\n")

	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")

		normalized := strings.TrimSpace(trimmed)
		if reHeaderNone.MatchString(normalized) {
			return []string{}, nil
		}
		if !reHeader.MatchString(normalized) {
			// A near-miss heading (e.g. "## Dependencies:", "##
			// Dependencies (blocked by)") must fail closed rather than be
			// silently treated as "no Dependencies block present" — that
			// would let an Issue schedule as if it had no prerequisites.
			if reNearMissHeader.MatchString(normalized) {
				return nil, fmt.Errorf(
					"tracker: invalid %q syntax: %q (expected exactly \"## Dependencies\" or \"## Dependencies: None\")",
					"## Dependencies", normalized,
				)
			}
			continue
		}

		// Found the bare "## Dependencies" heading; consume subsequent
		// lines until the next heading or end of body.
		var ids []string
		for _, bodyLine := range lines[i+1:] {
			t := strings.TrimSpace(strings.TrimRight(bodyLine, "\r"))
			if t == "" {
				continue
			}
			if reAnyHeading.MatchString(t) {
				break
			}
			// A Markdown thematic break ("---", "***", "___") closes the
			// section just like the next heading does — it is structural
			// markdown, not a dependency line, so stop consuming here.
			if reThematicBreak.MatchString(t) {
				break
			}
			switch {
			case reNoneLine.MatchString(t):
				// A bare "None" (or "None.") line inside the block is the
				// natural body form of "## Dependencies\nNone." and means the
				// same as "## Dependencies: None" — no prerequisites. Treat it
				// as an explicit empty list rather than freeform text.
				continue
			case reLabel.MatchString(t):
				continue
			case reInlineDepends.MatchString(t):
				m := reInlineDepends.FindStringSubmatch(t)
				for _, ref := range reIssueRef.FindAllStringSubmatch(m[1], -1) {
					ids = append(ids, ref[1])
				}
			case reBullet.MatchString(t):
				m := reBullet.FindStringSubmatch(t)
				// A single bullet declares a single dependency. If the
				// descriptive annotation itself contains another issue
				// ref, fail closed rather than silently drop it.
				if reIssueRef.MatchString(m[2]) {
					return nil, fmt.Errorf(
						"tracker: multiple issue refs on one dependency bullet in %q block: %q (use one \"- #123\" bullet per dependency)",
						"## Dependencies", t,
					)
				}
				ids = append(ids, m[1])
			default:
				return nil, fmt.Errorf(
					"tracker: invalid dependency syntax in %q block: %q (expected \"- #123\" or \"Depends on: #123\")",
					"## Dependencies", t,
				)
			}
		}
		return ids, nil
	}

	return []string{}, nil
}
