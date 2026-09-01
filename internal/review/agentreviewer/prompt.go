package agentreviewer

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/review"
)

// buildPolicyNotes composes one axis agent invocation's prompt: rubric (that
// axis's embedded rubric text) followed by the concrete review context (the
// Issue's requirements, the diff under review, and the Quality Gate results
// that already passed). agent.AgentRequest has no dedicated "skill"/
// "subagent" primitive (CLAUDE.md's ADR-0004 context, and the issue's own
// framing), so this is injected via AgentRequest.Policy.Notes, the one
// free-form guidance field the Agent contract exposes. Each axis gets its
// own call with its own rubric, so the three concurrent invocations are
// otherwise identical apart from this one substitution.
//
// parentSpec, when non-empty, is the content of a parent/spec Issue the
// Issue under review's body references (issue #319: a sub-issue's body is
// only a bare pointer to its parent, so cross-ticket intent living in the
// parent is otherwise invisible to the reviewer). It is resolved once per
// Review call by resolveParentSpec and injected under its own heading,
// ahead of the diff, so the reviewing agent reads it before judging the
// change. An empty parentSpec (no reference detected, no fetcher wired, or
// the fetch failed) omits the section entirely — existing behavior is
// unchanged when the Issue under review references no parent.
func buildPolicyNotes(req review.Request, rubric string, parentSpec string) string {
	var b strings.Builder
	b.WriteString(rubric)

	b.WriteString("\n\n## Issue under review\n\n")
	fmt.Fprintf(&b, "Title: %s\n\n", req.Issue.Title)
	if strings.TrimSpace(req.Issue.Body) != "" {
		b.WriteString(req.Issue.Body)
		b.WriteString("\n")
	}

	if parentSpec != "" {
		b.WriteString("\n## Parent spec\n\n")
		b.WriteString(parentSpec)
		b.WriteString("\n")
	}

	b.WriteString("\n## Diff under review\n\n```diff\n")
	b.WriteString(req.Diff)
	b.WriteString("\n```\n")

	if len(req.GateResults) > 0 {
		b.WriteString("\n## Quality Gate results (already passed)\n\n")
		for _, g := range req.GateResults {
			status := "passed"
			if !g.Passed {
				status = "failed"
			}
			fmt.Fprintf(&b, "- %s: %s\n", g.Name, status)
		}
	}

	return b.String()
}
