package claude

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
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
	"only when status is \"NEEDS_INFO\". If, while working this Issue, you " +
	"noticed a genuine inefficiency, bug, or edge case that is out of scope " +
	"for this Issue's own requirements, list each one as its own entry in " +
	"\"follow_ups\" (\"title\", \"body\") instead of expanding this Issue's " +
	"scope to address it — Forge files each entry as its own tracker Issue. " +
	"Leave \"follow_ups\" empty when there is nothing to report."

// rules is the fixed set of workflow-mechanics boundaries every invocation
// carries: the Agent does the engineering, Forge owns everything else (see
// CONTEXT.md "Agent").
const rules = "## Rules\n\n" +
	"- Implement the Issue's requirements completely and correctly in this Workspace.\n" +
	"- Do NOT create pull requests.\n" +
	"- Do NOT manage labels or other issue-tracker metadata.\n" +
	"- Do NOT decide workflow state; Forge's orchestrator owns all workflow mechanics.\n"

// reviewRules is implement-mode `rules`' read-only counterpart for
// agent.ModeReview (issue #183): a review invocation inspects the change and
// emits a report — it must not modify the Workspace, and it takes its output
// format from the rubric carried in the Workflow Policy rather than from the
// implement-mode {status, summary} result contract.
const reviewRules = "## Rules\n\n" +
	"- You are REVIEWING a change, not implementing one. Do NOT modify, create, or delete any files in this Workspace.\n" +
	"- Do NOT create pull requests or manage issue-tracker metadata.\n" +
	"- Do NOT decide workflow state; Forge's orchestrator owns all workflow mechanics.\n" +
	"- Produce your review by following the rubric in the Workflow Policy above, including its output contract, exactly: your final message must be that output and nothing else.\n"

// buildPrompt renders req into the prompt piped to Claude Code on stdin. It
// draws on whatever the normalized Issue and Repository/Policy context
// currently carry (see internal/agent.AgentRequest). A req in
// agent.ModeReview is rendered by buildReviewPrompt instead — a read-only
// analysis task with none of the implement/TDD/result-contract framing. A
// req in agent.ModeStructured returns req.Prompt verbatim (issue 200): the
// caller has already built the exact prompt it wants sent, so none of the
// Issue/Repository/Policy scaffolding below applies.
func buildPrompt(req agent.AgentRequest) string {
	if req.Mode == agent.ModeReview {
		return buildReviewPrompt(req)
	}
	if req.Mode == agent.ModeStructured {
		return req.Prompt
	}

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

	b.WriteString(clicommon.TDDGuidance)
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

// buildReviewPrompt renders an agent.ModeReview invocation (issue #183): a
// read-only review task. Unlike buildPrompt it omits every implement-mode
// element — the "Implement the Issue's requirements" rules, the TDD
// guidance, and the {status, summary} result contract — because those steer
// the agent toward writing a prose implementation summary, which the CLI's
// `--json-schema` enforcement (also omitted for review mode, see Execute)
// then locks in, clobbering the findings envelope the reviewer parses out of
// the final message. The concrete review context (rubric, Issue, diff, gate
// results) arrives via req.Policy.Notes, exactly as the implement-mode path
// renders Workflow Policy; the rubric there carries the output contract.
func buildReviewPrompt(req agent.AgentRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Forge Review: Issue %s\n\n", req.Issue.ID)

	b.WriteString("You are REVIEWING the changes in this Workspace for one review axis. ")
	b.WriteString("Inspect the change and produce a review report; do not modify anything.\n\n")

	b.WriteString("## Issue\n\n")
	fmt.Fprintf(&b, "- ID: %s\n", req.Issue.ID)
	if req.Issue.Title != "" {
		fmt.Fprintf(&b, "- Title: %s\n", req.Issue.Title)
	}
	b.WriteString("\n")
	if req.Issue.Body != "" {
		b.WriteString("### Description\n\n")
		b.WriteString(req.Issue.Body)
		b.WriteString("\n\n")
	}

	if req.Policy.Notes != "" {
		b.WriteString("## Workflow Policy\n\n")
		b.WriteString(req.Policy.Notes)
		b.WriteString("\n\n")
	}

	b.WriteString(reviewRules)
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
