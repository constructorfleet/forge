package review

import (
	"fmt"

	"github.com/Teagan42/forge/internal/domain"
)

// Signature returns Finding's stable repeat-detection identity (issue
// #375): Axis, Severity, File, Line, and Message together. Two Findings
// with the same Signature are the reviewer raising the identical objection
// again, not two independent findings that merely share one field.
func (f Finding) Signature() string {
	return fmt.Sprintf("%s|%s|%s:%d|%s", f.Axis, f.Severity, f.File, f.Line, f.Message)
}

// NonConvergent returns the Findings in current whose Signature also
// appears in previous: the review rejected this repair attempt for exactly
// the same reason as the one before it, meaning the axis and the Agent are
// not converging (issue #375). An empty previous — the first review run —
// never yields a non-convergent Finding.
func NonConvergent(current, previous []Finding) []Finding {
	if len(previous) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(previous))
	for _, f := range previous {
		seen[f.Signature()] = true
	}
	var repeated []Finding
	for _, f := range current {
		if seen[f.Signature()] {
			repeated = append(repeated, f)
		}
	}
	return repeated
}

// ApplyOverrides splits findings into remaining (not matched by any
// persisted domain.ReviewOverride, still blocking) and overridden (matched,
// suppressed) — issue #375's "persist a per-branch review override so it
// survives re-runs": a Finding Forge has already escalated as
// non-convergent for this Issue does not force another retry-budget cycle
// in a later Execution.
func ApplyOverrides(findings []Finding, overrides []domain.ReviewOverride) (remaining, overridden []Finding) {
	if len(overrides) == 0 {
		return findings, nil
	}
	blocked := make(map[string]bool, len(overrides))
	for _, o := range overrides {
		blocked[o.Signature] = true
	}
	for _, f := range findings {
		if blocked[f.Signature()] {
			overridden = append(overridden, f)
		} else {
			remaining = append(remaining, f)
		}
	}
	return remaining, overridden
}
