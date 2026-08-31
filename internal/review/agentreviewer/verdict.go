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

// combine folds every axis's axisOutcome into one review.Result: issue
// #160's deterministic synthesizer (synthesizeFindings in synthesizer.go)
// dedups matched cross-axis findings, folds their confidences, surfaces
// remedy conflicts as tensions instead of dropping either side, and ranks
// the merged set. The #158 verdict rule is then applied ONCE over that
// merged set: VerdictChangesRequired iff at least one merged Finding maps
// to review.SeverityError with Confidence >= confidenceFloor — recomputed
// on the merged confidence, not on any single axis's, so cross-axis
// agreement can push a finding neither axis was confident enough alone to
// block on over the floor (see TestCombine_AgreementLiftsConfidenceOverFloor_ChangesRequired).
func combine(outcomes []axisOutcome, confidenceFloor float64) review.Result {
	findings, tensions := synthesizeFindings(outcomes, confidenceFloor)

	blocked := false
	for _, f := range findings {
		if f.Severity == review.SeverityError && f.Confidence >= confidenceFloor {
			blocked = true
			break
		}
	}

	verdict := review.VerdictApproved
	if blocked {
		verdict = review.VerdictChangesRequired
	}

	return review.Result{
		Verdict:  verdict,
		Summary:  summarizeCombined(outcomes, verdict, findings, tensions),
		Findings: findings,
	}
}

// combineDegraded folds the surviving axes' outcomes into a coverage-honest
// review.Result when at least one axis in failedAxes could not be recovered
// within axisMaxAttempts in-place retries (issue #161). A false APPROVED is
// the worst outcome Forge can produce, so a degraded Review is never
// approved outright, even when every surviving axis is clean:
//
//   - a surviving blocker (merged Severity ERROR at/above confidenceFloor)
//     still forces review.VerdictChangesRequired, exactly as combine would
//     with full coverage — the loop must still act on what a healthy axis
//     actually found, rather than escalating past a real, actionable
//     finding;
//   - otherwise (survivors clean, or no axis survived at all) the Review
//     cannot certify full coverage, so it returns review.VerdictInconclusive
//     — never review.VerdictApproved on partial evidence, and never
//     VerdictChangesRequired with nothing concrete to act on. The engine
//     routes VerdictInconclusive to a human (NEEDS_INFO) rather than
//     treating it as either a pass or a repairable rejection.
func combineDegraded(survivors []axisOutcome, failedAxes []string, confidenceFloor float64) review.Result {
	findings, tensions := synthesizeFindings(survivors, confidenceFloor)

	blocked := false
	for _, f := range findings {
		if f.Severity == review.SeverityError && f.Confidence >= confidenceFloor {
			blocked = true
			break
		}
	}

	if blocked {
		verdict := review.VerdictChangesRequired
		return review.Result{
			Verdict: verdict,
			Summary: fmt.Sprintf("%s; degraded coverage: axis(es) %s unrecoverable, routed as %s on a surviving blocker",
				summarizeCombined(survivors, verdict, findings, tensions), strings.Join(failedAxes, ", "), verdict),
			Findings: findings,
		}
	}

	return review.Result{
		Verdict:  review.VerdictInconclusive,
		Summary:  summarizeInconclusive(survivors, failedAxes, findings),
		Findings: findings,
	}
}

// summarizeInconclusive produces the Result.Summary text for a degraded
// Review that could not certify full coverage: which axes survived clean,
// which axes are missing, and why escalation (not approval) is the correct
// outcome.
func summarizeInconclusive(survivors []axisOutcome, failedAxes []string, findings []review.Finding) string {
	survivorNames := make([]string, 0, len(survivors))
	for _, o := range survivors {
		survivorNames = append(survivorNames, o.axis)
	}
	survivorDesc := "none"
	if len(survivorNames) > 0 {
		survivorDesc = strings.Join(survivorNames, "+")
	}
	return fmt.Sprintf(
		"review incomplete: axis(es) %s unrecoverable after %d attempt(s) each; surviving axes (%s) clean (%d advisory finding(s)); escalating to a human rather than approving on partial coverage",
		strings.Join(failedAxes, ", "), axisMaxAttempts, survivorDesc, len(findings),
	)
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
// axis in outcomes, appending any synthesizeFindings tensions so remedy
// conflicts are surfaced to a human/Worker reading Summary rather than only
// being visible by diffing Findings (issue #160 acceptance criteria:
// tensions "surfaced... never silently dropped").
func summarizeCombined(outcomes []axisOutcome, verdict review.Verdict, findings []review.Finding, tensions []string) string {
	axisNames := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		axisNames = append(axisNames, o.axis)
	}
	base := fmt.Sprintf("axes %s: %d finding(s), verdict %s", strings.Join(axisNames, "+"), len(findings), verdict)
	if len(tensions) == 0 {
		return base
	}
	return fmt.Sprintf("%s; tensions: %s", base, strings.Join(tensions, "; "))
}
