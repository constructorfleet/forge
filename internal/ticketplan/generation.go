package ticketplan

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/planningagent"
)

type TicketGenResult struct {
	Key                string   `json:"key"`
	Objective          string   `json:"objective"`
	Requirements       []string `json:"requirements"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Dependencies       []string `json:"dependencies"`
}

type TicketPlanGenerationResult struct {
	Tickets []TicketGenResult `json:"tickets"`
}

type ticketPlanGenerationRequest struct {
	Context planningagent.PlanningContext `json:"context"`
}

func Generate(ctx context.Context, backend planningagent.Backend, pc planningagent.PlanningContext) (*TicketPlanGenerationResult, error) {
	req := ticketPlanGenerationRequest{Context: pc}

	res, err := planningagent.InvokeStructured(ctx, backend, "ticket-plan-generation", req,
		func(r ticketPlanGenerationRequest) string {
			return buildTicketPlanGenerationPrompt(r)
		},
		func(r TicketPlanGenerationResult) error {
			return validateTicketPlanGenerationResult(r)
		})
	if err != nil {
		return nil, fmt.Errorf("ticket-plan-generation invocation failed: %w", err)
	}

	return &res, nil
}

func buildTicketPlanGenerationPrompt(req ticketPlanGenerationRequest) string {
	var prompt string
	prompt += "# Ticket Plan Generation\n\n"
	prompt += "You are generating a ticket plan from an approved specification.\n\n"
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

	if req.Context.Spec != nil {
		prompt += "## Approved Specification\n"
		for heading, body := range req.Context.Spec.Sections {
			prompt += fmt.Sprintf("### %s\n%s\n\n", heading, body)
		}
	}

	prompt += "## Instructions\n"
	prompt += "Generate a ticket plan with temporary ticket keys (TKT-001, TKT-002, ...) that covers all requirements from the specification.\n"
	prompt += "Each ticket must have:\n"
	prompt += "1. **key** - Temporary ticket key (TKT-001, TKT-002, ...)\n"
	prompt += "2. **objective** - Clear, measurable objective for this ticket\n"
	prompt += "3. **requirements** - List of requirement IDs (REQ-NNN) this ticket addresses (at least one)\n"
	prompt += "4. **acceptance_criteria** - List of measurable acceptance criteria (at least one)\n"
	prompt += "5. **dependencies** - List of other ticket keys this ticket depends on (empty if none)\n\n"
	prompt += "Rules:\n"
	prompt += "- Keys must be sequential (TKT-001, TKT-002, ...)\n"
	prompt += "- Dependencies must only reference other ticket keys (TKT-NNN), never decision IDs\n"
	prompt += "- No self-dependencies or cycles\n"
	prompt += "- Every requirement from the spec must be covered by at least one ticket\n"
	prompt += "- Every ticket must reference at least one requirement\n\n"
	prompt += "Return your response as a JSON object in a fenced code block:\n"
	prompt += "```json\n"
	prompt += "{\n"
	prompt += `  "tickets": [` + "\n"
	prompt += `    {"key": "TKT-001", "objective": "...", "requirements": ["REQ-001"], "acceptance_criteria": ["..."], "dependencies": []},` + "\n"
	prompt += `    ...` + "\n"
	prompt += `  ]` + "\n"
	prompt += "}\n"
	prompt += "```\n"

	return prompt
}

func validateTicketPlanGenerationResult(r TicketPlanGenerationResult) error {
	if len(r.Tickets) == 0 {
		return fmt.Errorf("ticket plan generation produced no tickets")
	}

	expectedKey := 1
	seenKeys := make(map[string]bool)
	allReqs := make(map[string]bool)

	for i, t := range r.Tickets {
		expected := fmt.Sprintf("TKT-%03d", expectedKey)
		if t.Key != expected {
			return fmt.Errorf("ticket %d key %s out of sequence, expected %s", i, t.Key, expected)
		}
		if seenKeys[t.Key] {
			return fmt.Errorf("duplicate ticket key: %s", t.Key)
		}
		seenKeys[t.Key] = true

		if t.Objective == "" {
			return fmt.Errorf("ticket %s has empty objective", t.Key)
		}

		if len(t.Requirements) == 0 {
			return fmt.Errorf("ticket %s has no requirements", t.Key)
		}
		for _, req := range t.Requirements {
			if !reqIDPattern(req) {
				return fmt.Errorf("ticket %s has invalid requirement ID: %s", t.Key, req)
			}
			allReqs[req] = true
		}

		if len(t.AcceptanceCriteria) == 0 {
			return fmt.Errorf("ticket %s has no acceptance criteria", t.Key)
		}
		for _, ac := range t.AcceptanceCriteria {
			if ac == "" {
				return fmt.Errorf("ticket %s has empty acceptance criterion", t.Key)
			}
		}

		for _, dep := range t.Dependencies {
			if !ticketKeyPattern.MatchString(dep) {
				return fmt.Errorf("ticket %s has invalid dependency (must be TKT-NNN): %s", t.Key, dep)
			}
			if dep == t.Key {
				return fmt.Errorf("ticket %s has self-dependency", t.Key)
			}
		}

		expectedKey++
	}

	return nil
}
