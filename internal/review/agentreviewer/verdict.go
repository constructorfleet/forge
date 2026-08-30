package agentreviewer

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/review"
)

// axisName is the review axis this package implements (issue #158's
// tracer-bullet slice: bugs/breaking/security only — code-quality and docs
// are later tickets, #159/#160).
const axisName = "bugs"

// buildResult maps env onto a review.Result, applying the verdict rule:
// VerdictChangesRequired iff at least one Finding maps to review.SeverityError
// (axis HIGH) with Confidence >= confidenceFloor; otherwise VerdictApproved.
// Every Finding in env is carried into Result.Findings regardless of
// verdict, so a HIGH finding below the floor (or any MED/LOW finding) still
// surfaces as advisory signal on VerdictApproved rather than being silently
// dropped.
func buildResult(env envelope, confidenceFloor float64) review.Result {
	findings := make([]review.Finding, 0, len(env.Findings))
	blocked := false

	for _, f := range env.Findings {
		severity := review.MapAxisSeverity(review.AxisSeverity(strings.ToUpper(strings.TrimSpace(f.Severity))))
		if severity == review.SeverityError && f.Confidence >= confidenceFloor {
			blocked = true
		}
		findings = append(findings, review.Finding{
			Severity:   severity,
			File:       f.File,
			Line:       f.Line,
			Message:    composeMessage(f),
			Confidence: f.Confidence,
			Axis:       axisName,
			Remedy:     f.Remedy,
			AgreedBy:   1,
		})
	}

	verdict := review.VerdictApproved
	if blocked {
		verdict = review.VerdictChangesRequired
	}

	return review.Result{
		Verdict:  verdict,
		Summary:  summarize(axisName, verdict, findings),
		Findings: findings,
	}
}

// composeMessage folds one axisFinding's message and evidence into
// review.Finding's single Message field (review.Finding predates the
// envelope's dedicated evidence field, mirroring how agent.Feedback predates
// structured Findings — see review.BuildFeedback's doc comment for the same
// pattern).
func composeMessage(f axisFinding) string {
	msg := strings.TrimSpace(f.Message)
	evidence := strings.TrimSpace(f.Evidence)
	switch {
	case msg == "" && evidence == "":
		return ""
	case evidence == "":
		return msg
	case msg == "":
		return evidence
	default:
		return fmt.Sprintf("%s (evidence: %s)", msg, evidence)
	}
}

// summarize produces Result.Summary's human-readable text.
func summarize(axis string, verdict review.Verdict, findings []review.Finding) string {
	return fmt.Sprintf("%s axis: %d finding(s), verdict %s", axis, len(findings), verdict)
}
