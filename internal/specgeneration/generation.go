package specgeneration

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/planningagent"
)

type Requirement struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type SpecGenerationResult struct {
	Summary      string        `json:"summary"`
	Requirements []Requirement `json:"requirements"`
	NonGoals     []string      `json:"non_goals"`
	DecisionRefs []string      `json:"decision_refs"`
}

type specGenerationRequest struct {
	Context planningagent.PlanningContext `json:"context"`
}

func Generate(ctx context.Context, backend planningagent.Backend, pc planningagent.PlanningContext) (*SpecGenerationResult, error) {
	req := specGenerationRequest{Context: pc}

	res, err := planningagent.InvokeStructured(ctx, backend, "specification-generation", req,
		func(r specGenerationRequest) string {
			return buildSpecGenerationPrompt(r)
		},
		func(r SpecGenerationResult) error {
			return validateSpecGenerationResult(r)
		})
	if err != nil {
		return nil, fmt.Errorf("specification-generation invocation failed: %w", err)
	}

	return &res, nil
}

func buildSpecGenerationPrompt(req specGenerationRequest) string {
	var prompt string
	prompt += "# Specification Generation\n\n"
	prompt += "You are generating a specification for a feature based on the planning context.\n\n"
	prompt += "## Repository Context\n"
	prompt += fmt.Sprintf("Base Revision: %s\n\n", req.Context.Repository.BaseRevision)

	if req.Context.Goal != nil {
		prompt += "## Goal\n"
		for heading, body := range req.Context.Goal.Sections {
			prompt += fmt.Sprintf("### %s\n%s\n\n", heading, body)
		}
	}

	if len(req.Context.Decisions) > 0 {
		prompt += "## Resolved Decisions\n"
		for _, dec := range req.Context.Decisions {
			prompt += fmt.Sprintf("### %s (%s)\n", dec.ID, dec.Kind)
			for heading, body := range dec.Sections {
				prompt += fmt.Sprintf("#### %s\n%s\n\n", heading, body)
			}
		}
	}

	prompt += "## Instructions\n"
	prompt += "Generate a specification with the following structure:\n"
	prompt += "1. **Summary** - A brief overview of what this feature does\n"
	prompt += "2. **Requirements** - A list of requirements with stable IDs (REQ-001, REQ-002, ...)\n"
	prompt += "3. **Non-Goals** - Things explicitly NOT in scope\n"
	prompt += "4. **Decision Refs** - List of decision IDs that informed this spec\n\n"
	prompt += "Return your response as a JSON object with this shape:\n"
	prompt += "{\n"
	prompt += `  "summary": "...",` + "\n"
	prompt += `  "requirements": [{"id": "REQ-001", "description": "..."}, ...],` + "\n"
	prompt += `  "non_goals": ["...", ...],` + "\n"
	prompt += `  "decision_refs": ["...", ...]` + "\n"
	prompt += "}\n"

	return prompt
}

func validateSpecGenerationResult(r SpecGenerationResult) error {
	if r.Summary == "" {
		return fmt.Errorf("specification generation produced empty summary")
	}
	if len(r.Requirements) == 0 {
		return fmt.Errorf("specification generation produced no requirements")
	}

	expectedID := 1
	for _, req := range r.Requirements {
		expected := fmt.Sprintf("REQ-%03d", expectedID)
		if req.ID != expected {
			return fmt.Errorf("requirement ID %s out of sequence, expected %s", req.ID, expected)
		}
		if req.Description == "" {
			return fmt.Errorf("requirement %s has empty description", req.ID)
		}
		expectedID++
	}

	for _, ng := range r.NonGoals {
		if ng == "" {
			return fmt.Errorf("non-goal entry is empty")
		}
	}

	return nil
}
