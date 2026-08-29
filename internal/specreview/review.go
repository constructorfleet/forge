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

	prompt += "## Instructions\n"
	prompt += "Evaluate the specification against these criteria:\n"
	prompt += "1. **Completeness** - Does it cover all required sections (Context, Requirements, Non-Goals)?\n"
	prompt += "2. **Requirements Quality** - Are requirements specific, measurable, and traceable (REQ-NNN)?\n"
	prompt += "3. **Non-Goals Clarity** - Are out-of-scope items explicitly listed?\n"
	prompt += "4. **Decision Alignment** - Does the spec properly reflect resolved decisions?\n"
	prompt += "5. **Consistency** - Are there no contradictions between sections?\n\n"
	prompt += "Return your response as a JSON object in a fenced code block:\n"
	prompt += "```json\n"
	prompt += "{\n"
	prompt += `  "verdict": "APPROVED" | "CHANGES_REQUIRED",` + "\n"
	prompt += `  "summary": "...",` + "\n"
	prompt += `  "findings": [{"severity": "ERROR" | "WARNING" | "INFO", "file": "", "line": 0, "message": "..."}, ...]` + "\n"
	prompt += "}\n"
	prompt += "```\n\n"
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
