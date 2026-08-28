package claude

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

// resultContract is the instruction appended to every prompt telling Claude
// Code how to signal its outcome back to Forge: a single fenced JSON block,
// as the final thing it emits, matching structuredResult's shape.
const resultContract = "## Required output format\n\n" +
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
// currently carry (see internal/agent.AgentRequest); as later tickets
// (e.g. the tracker adapter and Repository Context compiler) enrich those
// types with issue title/body/acceptance-criteria fields, this function
// should be extended to surface them.
func buildPrompt(req agent.AgentRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Forge Task: Issue %s\n\n", req.Issue.ID)

	b.WriteString("## Issue\n\n")
	fmt.Fprintf(&b, "- ID: %s\n", req.Issue.ID)
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
