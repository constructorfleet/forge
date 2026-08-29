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
	"  }\n" +
	"}\n" +
	"```\n\n" +
	"Omit \"needs_info\" unless status is \"NEEDS_INFO\". Do not emit more " +
	"than one such block."

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

// BuildPrompt renders req into the prompt handed to a backend, identified
// by backendName only for readability in headers/comments — the content
// itself is backend-independent. It draws on whatever the normalized Issue
// and Repository/Policy context currently carry (see
// internal/agent.AgentRequest), and is shared by every CLI/HTTP Agent
// Adapter so the result contract and rules stay identical across backends.
func BuildPrompt(backendName string, req agent.AgentRequest) string {
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

// hasRepositoryContext reports whether repo carries anything worth
// rendering, so BuildPrompt can omit an empty "## Repository Context"
// header rather than emitting a section with nothing under it.
func hasRepositoryContext(repo agent.RepositoryContext) bool {
	return repo.BaseRevision != "" ||
		repo.ProjectStructure != "" ||
		repo.AgentInstructions != "" ||
		len(repo.QualityGates) > 0
}
