package specreview

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/planningagent"
)

// Verdict is a SpecificationReview's outcome.
type Verdict string

const (
	// VerdictApproved means the specification may proceed to human approval.
	VerdictApproved Verdict = "APPROVED"

	// VerdictChangesRequired means the specification must be revised.
	VerdictChangesRequired Verdict = "CHANGES_REQUIRED"
)

// Severity is how serious one Finding is.
type Severity string

const (
	SeverityInfo    Severity = "INFO"
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

// Finding is one structured issue a SpecificationReviewer raised against the spec.
type Finding struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Message  string   `json:"message"`
}

// Result is the structured outcome of one SpecificationReview.
type Result struct {
	Verdict  Verdict   `json:"verdict"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

type specReviewRequest struct {
	Context planningagent.PlanningContext `json:"context"`
}

func Review(ctx context.Context, backend planningagent.Backend, pc planningagent.PlanningContext) (*Result, error) {
	req := specReviewRequest{Context: pc}

	res, err := planningagent.InvokeStructured(ctx, backend, "specification-review", req,
		func(r specReviewRequest) string {
			return buildSpecReviewPrompt(r)
		},
		func(r Result) error {
			return validateSpecReviewResult(r)
		})
	if err != nil {
		return nil, fmt.Errorf("specification-review invocation failed: %w", err)
	}

	return &res, nil
}

func buildSpecReviewPrompt(req specReviewRequest) string {
	var prompt string
	prompt += "# Specification Review\n\n"
	prompt += "You are reviewing a specification for quality, completeness, and clarity. This is a fresh review — you have not seen any prior conversation about this specification.\n\n"
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
		prompt += "## Specification Under Review\n"
		for heading, body := range req.Context.Spec.Sections {
			prompt += fmt.Sprintf("### %s\n%s\n\n", heading, body)
		}
	}

	prompt += "## Already Mechanically Verified\n"
	prompt += "Before you see this specification, deterministic validation has already confirmed all of the following. Do not re-flag any of these — treat them as guaranteed true, and spend your judgment only on what a machine cannot check:\n"
	prompt += "- All required sections are present (Context, Requirements, Non-Goals).\n"
	prompt += "- All requirement IDs are uniquely and correctly formatted (REQ-NNN).\n"
	prompt += "- All blocking decisions are resolved (none open or needs_human).\n"
	prompt += "- The specification's derived_from provenance references match the goal, decision, and repository revisions actually supplied.\n\n"
	prompt += "## Instructions\n"
	prompt += "Evaluate the specification only against qualitative criteria deterministic validation cannot check:\n"
	prompt += "1. **Requirements Quality** - Are requirements specific, measurable, and traceable, not just present?\n"
	prompt += "2. **Non-Goals Clarity** - Are out-of-scope items explicitly and unambiguously listed?\n"
	prompt += "3. **Decision Alignment** - Does the spec substantively reflect resolved decisions (not merely reference them)?\n"
	prompt += "4. **Consistency** - Are there no contradictions between sections?\n\n"
	prompt += "To keep your verdict reproducible for identical input, apply these rules:\n"
	prompt += "- Base your verdict only on the specification text shown above; do not speculate about unstated requirements or hypothetical future needs.\n"
	prompt += "- Only raise a finding for a concrete, specific defect you can point to in the text. If you are unsure whether something is a genuine defect or a stylistic preference, do not raise it.\n"
	prompt += "- Prefer APPROVED when the specification is coherent and complete on its own terms, even if it could be marginally improved.\n\n"
	prompt += "Return your response as a JSON object with this shape:\n"
	prompt += "{\n"
	prompt += `  "verdict": "APPROVED" | "CHANGES_REQUIRED",` + "\n"
	prompt += `  "summary": "...",` + "\n"
	prompt += `  "findings": [{"severity": "ERROR" | "WARNING" | "INFO", "file": "", "line": 0, "message": "..."}, ...]` + "\n"
	prompt += "}\n\n"
	prompt += "Rules:\n"
	prompt += "- If verdict is APPROVED, findings may be empty or contain only INFO findings\n"
	prompt += "- If verdict is CHANGES_REQUIRED, findings MUST contain at least one ERROR or WARNING finding\n"
	prompt += "- File and line are optional (use empty string and 0 for general findings)\n"

	return prompt
}

func validateSpecReviewResult(r Result) error {
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
