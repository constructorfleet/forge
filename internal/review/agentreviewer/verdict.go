package agentreviewer

import (
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/review"
)

// findingsForAxis maps one axis's parsed envelope onto review.Finding,
// tagging every Finding with axis (the axis this Reviewer intended to run,
// not env.Axis — the agent's self-reported axis field is informational
// only and never trusted for routing). It also reports blocked: whether any
// Finding in env maps to review.SeverityError (axis HIGH) with Confidence
// at or above confidenceFloor, the #158 verdict-blocking rule applied per
// axis before combine folds every axis's blocked flag together.
func findingsForAxis(env envelope, axis string, confidenceFloor float64) ([]review.Finding, bool) {
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
			Axis:       axis,
			Remedy:     f.Remedy,
			AgreedBy:   1,
		})
	}

	return findings, blocked
}

// buildResult maps one axis's envelope onto a standalone review.Result,
// applying the verdict rule: VerdictChangesRequired iff at least one
// Finding maps to review.SeverityError (axis HIGH) with Confidence >=
// confidenceFloor; otherwise VerdictApproved. Every Finding in env is
// carried into Result.Findings regardless of verdict, so a HIGH finding
// below the floor (or any MED/LOW finding) still surfaces as advisory
// signal on VerdictApproved rather than being silently dropped.
//
// This is used directly by verdict_test.go's per-axis unit tests; Reviewer's
// concurrent fan-out itself calls findingsForAxis and combine (below), since
// it needs one verdict computed over all three axes' findings combined, not
// three separate per-axis verdicts.
func buildResult(env envelope, axis string, confidenceFloor float64) review.Result {
	findings, blocked := findingsForAxis(env, axis, confidenceFloor)

	verdict := review.VerdictApproved
	if blocked {
		verdict = review.VerdictChangesRequired
	}

	return review.Result{
		Verdict:  verdict,
		Summary:  summarize(axis, verdict, findings),
		Findings: findings,
	}
}

// axisOutcome is one axis's name paired with its parsed envelope, as
// collected by Reviewer.Review's concurrent fan-out for combine to fold
// into one Result.
type axisOutcome struct {
	axis string
	env  envelope
}

// combine folds every axis's axisOutcome into one review.Result: this
// issue's (#159) simple combine is a straight concatenation of each axis's
// findings, in fixed axis order (bugs, quality, docs — see the axes slice
// in agentreviewer.go), followed by the #158 verdict rule applied ONCE over
// the combined set: VerdictChangesRequired iff at least one combined
// Finding maps to review.SeverityError with Confidence >= confidenceFloor.
//
// This is deliberately the whole synthesis step for now: no cross-axis
// dedup, confidence-fold, ranking, or tension detection. That is a
// separate, later ticket (#160, the deterministic synthesizer) and will
// replace this function's body — the axisOutcome slice it consumes is the
// seam a real synthesizer plugs into without Reviewer.Review needing to
// change.
func combine(outcomes []axisOutcome, confidenceFloor float64) review.Result {
	var findings []review.Finding
	blocked := false

	for _, o := range outcomes {
		axisFindings, axisBlocked := findingsForAxis(o.env, o.axis, confidenceFloor)
		findings = append(findings, axisFindings...)
		if axisBlocked {
			blocked = true
		}
	}

	verdict := review.VerdictApproved
	if blocked {
		verdict = review.VerdictChangesRequired
	}

	return review.Result{
		Verdict:  verdict,
		Summary:  summarizeCombined(outcomes, verdict, findings),
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

// summarize produces one axis's standalone Result.Summary text.
func summarize(axis string, verdict review.Verdict, findings []review.Finding) string {
	return fmt.Sprintf("%s axis: %d finding(s), verdict %s", axis, len(findings), verdict)
}

// summarizeCombined produces the combined Result.Summary text across every
// axis in outcomes.
func summarizeCombined(outcomes []axisOutcome, verdict review.Verdict, findings []review.Finding) string {
	axisNames := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		axisNames = append(axisNames, o.axis)
	}
	return fmt.Sprintf("axes %s: %d finding(s), verdict %s", strings.Join(axisNames, "+"), len(findings), verdict)
}
