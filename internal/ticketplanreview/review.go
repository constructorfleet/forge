package ticketplanreview

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/planningagent"
)

// Verdict is a TicketPlanReview's outcome.
type Verdict string

const (
	// VerdictApproved means the ticket plan may proceed to human approval.
	VerdictApproved Verdict = "APPROVED"

	// VerdictChangesRequired means the ticket plan must be revised.
	VerdictChangesRequired Verdict = "CHANGES_REQUIRED"
)

// Severity is how serious one Finding is.
type Severity string

const (
	SeverityInfo    Severity = "INFO"
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

// Finding is one structured issue a TicketPlanReviewer raised against the ticket plan.
// References temporary ticket keys (TKT-NNN) and/or requirement IDs (REQ-NNN).
type Finding struct {
	Severity    Severity `json:"severity"`
	TicketKey   string   `json:"ticket_key,omitempty"`
	Requirement string   `json:"requirement,omitempty"`
	Message     string   `json:"message"`
}

// Result is the structured outcome of one TicketPlanReview.
type Result struct {
	Verdict  Verdict   `json:"verdict"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

type ticketPlanReviewRequest struct {
	Context      planningagent.PlanningContext `json:"context"`
	TicketPlan   string                        `json:"ticket_plan"`
	SpecReqIDs   []string                      `json:"spec_req_ids"`
	SpecRevision string                        `json:"spec_revision"`
}

func Review(ctx context.Context, backend planningagent.Backend, pc planningagent.PlanningContext, ticketPlan string, specReqIDs []string, specRevision string) (*Result, error) {
	req := ticketPlanReviewRequest{
		Context:      pc,
		TicketPlan:   ticketPlan,
		SpecReqIDs:   specReqIDs,
		SpecRevision: specRevision,
	}

	res, err := planningagent.InvokeStructured(ctx, backend, "ticket-plan-review", req,
		func(r ticketPlanReviewRequest) string {
			return buildTicketPlanReviewPrompt(r)
		},
		func(r Result) error {
			return validateTicketPlanReviewResult(r)
		})
	if err != nil {
		return nil, fmt.Errorf("ticket-plan-review invocation failed: %w", err)
	}

	return &res, nil
}

func buildTicketPlanReviewPrompt(req ticketPlanReviewRequest) string {
	var prompt string
	prompt += "# Ticket Plan Review\n\n"
	prompt += "You are reviewing a ticket plan for quality, sizing, boundaries, coupling, and coverage. This is a fresh review — you have not seen any prior conversation about this ticket plan.\n\n"
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

	prompt += "## Ticket Plan Under Review\n"
	prompt += req.TicketPlan + "\n\n"

	prompt += "## Instructions\n"
	prompt += "Evaluate the ticket plan against these criteria:\n"
	prompt += "1. **Coverage** - Does every specification requirement (REQ-NNN) map to at least one ticket?\n"
	prompt += "2. **Ticket Sizing** - Is each ticket scoped to a single atomic engineering outcome that a frontier coding model could implement, test, and validate in approximately 10 minutes of execution time? Flag any ticket that bundles several independently implementable changes, spans multiple subsystems, introduces several independent behaviors, or would require substantial investigation before implementation could begin — it must be split into multiple smaller, explicitly dependent tickets instead.\n"
	prompt += "3. **Boundaries** - Do tickets have clear, non-overlapping responsibilities? No unrelated responsibilities combined in one ticket.\n"
	prompt += "4. **Unnecessary Sequencing** - Are dependencies minimal? Avoid artificial ordering where parallel work is possible.\n"
	prompt += "5. **Coupling** - Are tickets loosely coupled? High coupling between tickets suggests poor boundaries.\n"
	prompt += "6. **Acceptance Criteria** - Are acceptance criteria specific, measurable, and verifiable? Not vague.\n"
	prompt += "7. **Requirement Traceability** - Does each ticket reference valid REQ-NNN IDs from the spec?\n"
	prompt += "8. **Implementation Context** - Where files, directories, symbols, existing implementations, or analogous examples can reasonably be identified from the spec and repository context, does the ticket name them so the implementer does not need to rediscover the architecture or basic approach from scratch?\n\n"
	prompt += "Flag executable tickets whose only deliverable is verification-only, tracker-only, or otherwise cannot produce a git diff; those outcomes belong in acceptance criteria for code-changing tickets or outside the executable plan.\n\n"
	prompt += "Return your response as a JSON object with this shape:\n"
	prompt += "{\n"
	prompt += `  "verdict": "APPROVED" | "CHANGES_REQUIRED",` + "\n"
	prompt += `  "summary": "...",` + "\n"
	prompt += `  "findings": [{"severity": "ERROR" | "WARNING" | "INFO", "ticket_key": "TKT-001", "requirement": "REQ-001", "message": "..."}, ...]` + "\n"
	prompt += "}\n\n"
	prompt += "Rules:\n"
	prompt += "- If verdict is APPROVED, findings may be empty or contain only INFO findings\n"
	prompt += "- If verdict is CHANGES_REQUIRED, findings MUST contain at least one ERROR or WARNING finding\n"
	prompt += "- ticket_key and requirement are optional but encouraged for traceability\n"
	prompt += "- Reference temporary ticket keys (TKT-NNN) and requirement IDs (REQ-NNN) where applicable\n"

	return prompt
}

func validateTicketPlanReviewResult(r Result) error {
	if r.Verdict != VerdictApproved && r.Verdict != VerdictChangesRequired {
		return fmt.Errorf("invalid verdict: %q (must be APPROVED or CHANGES_REQUIRED)", r.Verdict)
	}

	if r.Summary == "" {
		return fmt.Errorf("summary must not be empty")
	}

	if r.Verdict == VerdictChangesRequired {
		if len(r.Findings) == 0 {
			return fmt.Errorf("CHANGES_REQUIRED must have at least one finding")
		}
		hasActionable := false
		for _, f := range r.Findings {
			if f.Severity == SeverityError || f.Severity == SeverityWarning {
				hasActionable = true
				break
			}
		}
		if !hasActionable {
			return fmt.Errorf("CHANGES_REQUIRED must have at least one ERROR or WARNING finding")
		}
	}

	for _, f := range r.Findings {
		if f.Severity != SeverityInfo && f.Severity != SeverityWarning && f.Severity != SeverityError {
			return fmt.Errorf("invalid finding severity: %q", f.Severity)
		}
		if f.Message == "" {
			return fmt.Errorf("finding message must not be empty")
		}
	}

	return nil
}
