package clicommon

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// ResultContract is the instruction appended to every prompt telling a
// backend how to signal its outcome back to Forge: a single fenced JSON
// block, as the final thing it emits, matching StructuredResult's shape.
const ResultContract = "## Required output format\n\n" +
	"When you are done — whether you completed the work, need information " +
	"from a human, or cannot proceed — emit exactly one fenced JSON code " +
	"block as the LAST thing in your response, matching this shape:\n\n" +
	"```json\n" +
	"{\n" +
	"  \"status\": \"IMPLEMENTED\" | \"NEEDS_INFO\" | \"FAILED\",\n" +
	"  \"summary\": \"one paragraph describing what happened\",\n" +
	"  \"needs_info\": {\n" +
	"    \"question\": \"what you need answered (only if status is NEEDS_INFO)\",\n" +
	"    \"context\": \"why the question arose (only if status is NEEDS_INFO)\"\n" +
	"  },\n" +
	"  \"follow_ups\": [\n" +
	"    {\"title\": \"one-line summary\", \"body\": \"what you noticed, why it is out of scope, and supporting detail\"}\n" +
	"  ]\n" +
	"}\n" +
	"```\n\n" +
	"Omit \"needs_info\" unless status is \"NEEDS_INFO\". Omit \"follow_ups\" " +
	"(or leave it empty) unless, while working this Issue, you noticed a " +
	"genuine inefficiency, bug, or edge case that is out of scope for this " +
	"Issue's requirements — list each one as a separate entry so Forge can " +
	"file it as its own tracker Issue; do not use it to expand this Issue's " +
	"own scope. Do not emit more than one such block."

// Rules is the fixed set of workflow-mechanics boundaries every invocation
// carries: the Agent does the engineering, Forge owns everything else (see
// CONTEXT.md "Agent").
const Rules = "## Rules\n\n" +
	"- Implement the Issue's requirements completely and correctly in this Workspace.\n" +
	"- Do NOT create pull requests.\n" +
	"- Do NOT manage labels or other issue-tracker metadata.\n" +
	"- Do NOT decide workflow state; Forge's orchestrator owns all workflow mechanics.\n"

// TDDGuidance instructs every backend to implement the Issue test-first,
// via the red (failing test) -> green (minimal implementation) loop, so
// Issues are always implemented test-driven regardless of which Agent
// Adapter runs them (see issue 105).
const TDDGuidance = "## Development Approach\n\n" +
	"Implement this Issue using Test-Driven Development: the red -> green " +
	"loop. For each slice of behavior, write a failing test before writing " +
	"the implementation that makes it pass, then move to the next slice. " +
	"Do not write implementation code ahead of a test, and do not write a " +
	"batch of tests before implementing any of them.\n\n" +
	"- Test observable behavior through public interfaces (seams), not " +
	"internal implementation details.\n" +
	"- Mock only at true system boundaries (external APIs, databases, " +
	"time/randomness) — never your own code or internal collaborators.\n" +
	"- Avoid tautological tests whose expected value is computed the same " +
	"way the implementation computes it; use independent, known-good " +
	"expected values.\n" +
	"- Work in vertical slices: one test, one minimal implementation, " +
	"repeat. Refactoring happens after the loop, not during it.\n"

// ModeResult applies the shared result semantics for the non-implement
// Modes — the single place every backend (the CLI trio via ExecuteCLI,
// openai, and internal/agent/claude) decides what a ModeReview/ModeStructured
// invocation returns. For those modes the agent's reconstructed final message
// (finalText) IS the deliverable — a review findings envelope, or a
// caller-schema-conforming result — returned verbatim as Summary; an empty
// final message is the one failure this framing detects on its own, surfaced
// as FAILED so a per-axis / per-call retry can react. handled is false for
// ModeImplement, where the caller runs its own {status, summary} parsing.
// Callers must apply this only after their transport-level guards
// (ctx/timeout/subprocess errors) have passed.
func ModeResult(backendName string, mode agent.AgentMode, finalText, stdout, stderr string, exitCode int) (agent.AgentResult, bool) {
	if mode != agent.ModeReview && mode != agent.ModeStructured {
		return agent.AgentResult{}, false
	}
	if strings.TrimSpace(finalText) == "" {
		label := "review"
		if mode == agent.ModeStructured {
			label = "structured"
		}
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: DiagnosticSummary(fmt.Sprintf("%s adapter: %s produced no output (exit code %d)", backendName, label, exitCode), stdout, stderr),
		}, true
	}
	return agent.AgentResult{Status: agent.StatusImplemented, Summary: finalText}, true
}

// reviewRules is implement-mode Rules' read-only counterpart for
// agent.ModeReview (mirrors internal/agent/claude's reviewRules): the agent
// inspects the change and follows the rubric's output contract instead of
// implementing anything.
const reviewRules = "## Rules\n\n" +
	"- You are REVIEWING a change, not implementing one. Do NOT modify, create, or delete any files in this Workspace.\n" +
	"- Do NOT create pull requests or manage issue-tracker metadata.\n" +
	"- Do NOT decide workflow state; Forge's orchestrator owns all workflow mechanics.\n" +
	"- Produce your review by following the rubric in the Workflow Policy above, including its output contract, exactly: your final message must be that output and nothing else.\n"

// BuildPrompt renders req into the prompt handed to a backend, identified
// by backendName only for readability in headers/comments — the content
// itself is backend-independent. It draws on whatever the normalized Issue
// and Repository/Policy context currently carry (see
// internal/agent.AgentRequest), and is shared by every CLI/HTTP Agent
// Adapter so the result contract and rules stay identical across backends.
//
// Mode selects the framing, matching internal/agent/claude: ModeStructured
// returns req.Prompt verbatim (the caller owns the whole prompt and schema);
// ModeReview renders a read-only review task whose output contract comes from
// the rubric in req.Policy.Notes, omitting the implement-mode rules, TDD
// guidance, and {status, summary} result contract that would otherwise
// clobber the findings envelope the reviewer parses. ModeImplement (the zero
// value) is the default implementation framing.
func BuildPrompt(backendName string, req agent.AgentRequest) string {
	switch req.Mode {
	case agent.ModeStructured:
		return req.Prompt
	case agent.ModeReview:
		return BuildReviewPrompt(req)
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

	b.WriteString(Rules)
	b.WriteString("\n")

	b.WriteString(TDDGuidance)
	b.WriteString("\n")

	if len(req.Feedback) > 0 {
		b.WriteString("## Feedback From Prior Attempt\n\n")
		b.WriteString("Address the following before continuing:\n\n")
		for _, fb := range req.Feedback {
			fmt.Fprintf(&b, "- [%s] %s\n", fb.Source, fb.Message)
		}
		b.WriteString("\n")
	}

	b.WriteString(ResultContract)
	b.WriteString("\n")

	return b.String()
}

// BuildReviewPrompt renders an agent.ModeReview invocation — the single
// shared review-prompt builder used by every backend (the CLI trio and
// openai via BuildPrompt's dispatch, and internal/agent/claude directly).
// A read-only review task that omits every implement-mode element — the
// "implement the requirements" rules, TDD guidance, and {status, summary}
// result contract — because those steer the agent toward a prose summary and
// clobber the findings envelope the reviewer parses out of the final message.
// The concrete review context (rubric, Issue, diff, gate results) arrives via
// req.Policy.Notes, exactly as the implement path renders Workflow Policy;
// the rubric there carries the output contract.
func BuildReviewPrompt(req agent.AgentRequest) string {
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
// rendering, so BuildPrompt can omit an empty "## Repository Context"
// header rather than emitting a section with nothing under it.
func hasRepositoryContext(repo agent.RepositoryContext) bool {
	return repo.BaseRevision != "" ||
		repo.ProjectStructure != "" ||
		repo.AgentInstructions != "" ||
		len(repo.QualityGates) > 0
}
