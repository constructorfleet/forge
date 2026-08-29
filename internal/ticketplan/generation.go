package ticketplan

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
)

type TicketGenResult struct {
	Key                   string                   `json:"key"`
	Objective             string                   `json:"objective"`
	Requirements          []string                 `json:"requirements"`
	AcceptanceCriteria    []string                 `json:"acceptance_criteria"`
	Dependencies          []string                 `json:"dependencies"`
	ImplementationContext []string                 `json:"implementation_context,omitempty"`
	Estimate              *planning.TicketEstimate `json:"estimate,omitempty"`
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
	prompt += "5. **dependencies** - List of other ticket keys this ticket depends on (empty if none)\n"
	prompt += "6. **implementation_context** - List of concrete starting points for the implementer: likely files, directories, symbols, existing implementations, or analogous examples already in the repository. Include this whenever such pointers can be identified during planning; leave it empty only when no such pointers exist.\n"
	prompt += "7. **estimate** - Optional effort/complexity estimate with:\n"
	prompt += "   - **size** - One of: S, M, L, XL (required if estimate provided)\n"
	prompt += "   - **risk** - Optional risk hint (e.g., \"new_tech\", \"unknown_deps\", \"complex_refactor\")\n\n"
	prompt += "Sizing and scope:\n"
	prompt += "- Scope each ticket to a single atomic engineering outcome, not a restatement of a broad spec requirement.\n"
	prompt += "- Each ticket must be implementable, testable, and validatable by a frontier coding model in approximately 10 minutes of execution time.\n"
	prompt += "- Prefer multiple small dependent tickets over one broad ticket containing several independently implementable changes; express sequencing with explicit dependencies.\n"
	prompt += "- Split a ticket further when it spans multiple subsystems, introduces several independent behaviors, or would require substantial investigation before implementation could begin.\n"
	prompt += "- A ticket should describe what concrete change to make and where to start, giving the implementer enough context to begin without rediscovering the architecture or the basic approach from scratch.\n\n"
	prompt += "Rules:\n"
	prompt += "- Keys must be sequential (TKT-001, TKT-002, ...)\n"
	prompt += "- Dependencies must only reference other ticket keys (TKT-NNN), never decision IDs\n"
	prompt += "- No self-dependencies or cycles\n"
	prompt += "- Every requirement from the spec must be covered by at least one ticket\n"
	prompt += "- Every ticket must reference at least one requirement\n"
	prompt += "- If estimate is provided, size must be S, M, L, or XL\n\n"
	prompt += "Return your response as a JSON object in a fenced code block:\n"
	prompt += "```json\n"
	prompt += "{\n"
	prompt += `  "tickets": [` + "\n"
	prompt += `    {"key": "TKT-001", "objective": "...", "requirements": ["REQ-001"], "acceptance_criteria": ["..."], "dependencies": [], "implementation_context": ["internal/foo/bar.go: extend Baz() with ..."], "estimate": {"size": "M", "risk": "new_tech"}},` + "\n"
	prompt += `    ...` + "\n"
	prompt += `  ]` + "\n"
	prompt += "}\n"
	prompt += "```\n"

	return prompt
}

// RenderTicketBody renders a generated ticket's section body in the standard
// ticket-plan markdown layout (Objective / Requirements / Acceptance Criteria /
// Implementation Context / Dependencies).
func RenderTicketBody(t TicketGenResult) string {
	body := fmt.Sprintf("### Objective\n%s\n\n### Requirements\n", t.Objective)
	for _, req := range t.Requirements {
		body += fmt.Sprintf("%s\n", req)
	}
	body += "\n### Acceptance Criteria\n"
	for _, ac := range t.AcceptanceCriteria {
		body += fmt.Sprintf("- %s\n", ac)
	}
	body += "\n### Implementation Context\n"
	if len(t.ImplementationContext) == 0 {
		body += "None"
	} else {
		for _, note := range t.ImplementationContext {
			body += fmt.Sprintf("- %s\n", note)
		}
	}
	body += "\n\n### Dependencies\n"
	if len(t.Dependencies) == 0 {
		body += "None"
	} else {
		for _, dep := range t.Dependencies {
			body += fmt.Sprintf("%s\n", dep)
		}
	}
	return body
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

		for _, note := range t.ImplementationContext {
			if note == "" {
				return fmt.Errorf("ticket %s has empty implementation context entry", t.Key)
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

		if t.Estimate != nil {
			if t.Estimate.Size == "" {
				return fmt.Errorf("ticket %s has estimate with empty size", t.Key)
			}
			if !planning.ValidEstimateSizes[t.Estimate.Size] {
				return fmt.Errorf("ticket %s has invalid estimate size %q (must be S, M, L, or XL)", t.Key, t.Estimate.Size)
			}
		}

		expectedKey++
	}

	return nil
}
