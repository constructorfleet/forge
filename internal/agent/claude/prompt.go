package claude

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// resultContract is the instruction appended to every prompt telling Claude
// Code how to signal its outcome back to Forge. Since issue 20/ticket 32,
// the envelope's shape (status/summary/needs_info/usage) is enforced by the
// CLI itself via `--json-schema` (see resultJSONSchema in result.go) rather
// than by prose here, so this is reduced to a pointer covering only what
// the schema can't express: which status means what, and when to populate
// needs_info.
const resultContract = "## Result\n\n" +
	"Report your outcome as \"status\": \"IMPLEMENTED\" once you have " +
	"completed the work, \"NEEDS_INFO\" if you need information from a " +
	"human before you can proceed, or \"FAILED\" if you cannot complete " +
	"the work. Populate \"needs_info\" (with \"question\" and \"context\") " +
	"only when status is \"NEEDS_INFO\"."

// rules is the fixed set of workflow-mechanics boundaries every invocation
// carries: the Agent does the engineering, Forge owns everything else (see
// CONTEXT.md "Agent").
const rules = "## Rules\n\n" +
	"- Implement the Issue's requirements completely and correctly in this Workspace.\n" +
	"- Do NOT create pull requests.\n" +
	"- Do NOT manage labels or other issue-tracker metadata.\n" +
	"- Do NOT decide workflow state; Forge's orchestrator owns all workflow mechanics.\n"

// buildPrompt renders req into the prompt piped to Claude Code on stdin. It
// draws on whatever the normalized Issue and Repository/Policy context
// currently carry (see internal/agent.AgentRequest).
func buildPrompt(req agent.AgentRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Forge Task: Issue %s\n\n", req.Issue.ID)

	b.WriteString("## Issue\n\n")
	fmt.Fprintf(&b, "- ID: %s\n", req.Issue.ID)
	if req.Issue.Title != "" {
		fmt.Fprintf(&b, "- Title: %s\n", req.Issue.Title)
	}
	fmt.Fprintf(&b, "- State: %s\n", req.Issue.State)
	if len(req.Issue.Dependencies) > 0 {
		b.WriteString("- Depends on: ")
		for i, dep := range req.Issue.Dependencies {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(dep.DependsOnID)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("- Depends on: none\n")
	}
	b.WriteString("\n")

	if req.Issue.Body != "" {
		b.WriteString("### Description\n\n")
		b.WriteString(req.Issue.Body)
		b.WriteString("\n\n")
	}

	if hasRepositoryContext(req.Repository) {
		b.WriteString("## Repository Context\n\n")
		if req.Repository.BaseRevision != "" {
			fmt.Fprintf(&b, "Base revision: %s\n\n", req.Repository.BaseRevision)
		}
		if req.Repository.ProjectStructure != "" {
			b.WriteString("### Project Structure\n\n")
			b.WriteString(req.Repository.ProjectStructure)
			b.WriteString("\n\n")
		}
		if req.Repository.AgentInstructions != "" {
			b.WriteString("### Agent Instructions\n\n")
			b.WriteString(req.Repository.AgentInstructions)
			b.WriteString("\n\n")
		}
		if len(req.Repository.QualityGates) > 0 {
			b.WriteString("### Quality Gates\n\n")
			b.WriteString("Your implementation must pass all of the following before it is considered done:\n\n")
			for _, gate := range req.Repository.QualityGates {
				fmt.Fprintf(&b, "- %s\n", gate)
			}
			b.WriteString("\n")
		}
	}

	if req.Policy.Notes != "" {
		b.WriteString("## Workflow Policy\n\n")
		b.WriteString(req.Policy.Notes)
		b.WriteString("\n\n")
	}

	b.WriteString(rules)
	b.WriteString("\n")

	if len(req.Feedback) > 0 {
		b.WriteString("## Feedback From Prior Attempt\n\n")
		b.WriteString("Address the following before continuing:\n\n")
		for _, fb := range req.Feedback {
			fmt.Fprintf(&b, "- [%s] %s\n", fb.Source, fb.Message)
		}
		b.WriteString("\n")
	}

	b.WriteString(resultContract)
	b.WriteString("\n")

	return b.String()
}

// hasRepositoryContext reports whether repo carries anything worth
// rendering, so buildPrompt can omit an empty "## Repository Context"
// header rather than emitting a section with nothing under it.
func hasRepositoryContext(repo agent.RepositoryContext) bool {
	return repo.BaseRevision != "" ||
		repo.ProjectStructure != "" ||
		repo.AgentInstructions != "" ||
		len(repo.QualityGates) > 0
}
