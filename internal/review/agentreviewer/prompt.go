package agentreviewer

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/review"
)

// buildPolicyNotes composes the fresh axis agent invocation's prompt: the
// embedded rubric followed by the concrete review context (the Issue's
// requirements, the diff under review, and the Quality Gate results that
// already passed). agent.AgentRequest has no dedicated "skill"/"subagent"
// primitive (CLAUDE.md's ADR-0004 context, and the issue's own framing), so
// this is injected via AgentRequest.Policy.Notes, the one free-form guidance
// field the Agent contract exposes.
func buildPolicyNotes(req review.Request) string {
	var b strings.Builder
	b.WriteString(rubric)

	b.WriteString("\n\n## Issue under review\n\n")
	fmt.Fprintf(&b, "Title: %s\n\n", req.Issue.Title)
	if strings.TrimSpace(req.Issue.Body) != "" {
		b.WriteString(req.Issue.Body)
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
