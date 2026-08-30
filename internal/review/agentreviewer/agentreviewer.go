// Package agentreviewer is Forge's production, single-axis review.Reviewer
// (issue #158): it runs the bugs/breaking/security axis as one fresh
// agent.Agent invocation per Review call (ADR-0004: no implementation
// conversation — see internal/review's package doc), with the axis's rubric
// embedded in the binary via go:embed and injected through
// agent.AgentRequest.Policy.Notes (the only free-form guidance primitive
// AgentRequest exposes).
//
// The axis agent is instructed to emit a review-shaped JSON findings
// envelope as its final output; since agent.AgentResult carries no
// dedicated findings field (Summary is its only text surface), Reviewer
// parses that envelope out of AgentResult.Summary (see envelope.go) and maps
// it onto review.Result via the verdict rule (see verdict.go): a Finding
// that maps to review.SeverityError with Confidence at or above
// ConfidenceFloor forces review.VerdictChangesRequired; otherwise the Review
// is review.VerdictApproved, with any lower-severity or below-floor findings
// still attached to Result.Findings as advisory signal.
//
// This is the tracer-bullet slice from issue #158: only the bugs axis runs
// here. The other axes (code-quality issue #159, docs issue #160), the
// parallel fan-out and deterministic synthesizer across axes, degradation
// handling for a malformed envelope, and full audit persistence are all
// separate, later tickets (#159-#162) — this package deliberately does none
// of that.
package agentreviewer

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/review"
)

//go:embed rubric.md
var rubric string

// defaultConfidenceFloor mirrors config.Default's
// Workflow.ReviewConfidenceFloor default, so a Reviewer constructed with
// confidenceFloor <= 0 (e.g. directly in a test, bypassing config) still
// behaves sensibly rather than blocking on every ERROR-severity finding
// regardless of confidence.
const defaultConfidenceFloor = 0.7

// var declaration ensures Reviewer satisfies review.Reviewer at compile
// time.
var _ review.Reviewer = (*Reviewer)(nil)

// Reviewer is the production single-axis (bugs/breaking/security)
// review.Reviewer. It holds the agent.Agent used to run the axis and the
// confidence floor gating its verdict.
type Reviewer struct {
	// Agent runs the axis as a fresh, independent invocation per Review
	// call.
	Agent agent.Agent

	// ConfidenceFloor is the minimum Confidence (0.0-1.0) an ERROR-severity
	// Finding must carry to force VerdictChangesRequired (config field
	// workflow.review_confidence_floor). <= 0 uses defaultConfidenceFloor.
	ConfidenceFloor float64
}

// New returns a Reviewer running axis reviews via a, gating verdicts at
// confidenceFloor. confidenceFloor <= 0 uses defaultConfidenceFloor.
func New(a agent.Agent, confidenceFloor float64) *Reviewer {
	if confidenceFloor <= 0 {
		confidenceFloor = defaultConfidenceFloor
	}
	return &Reviewer{Agent: a, ConfidenceFloor: confidenceFloor}
}

// Review runs the bugs/breaking/security axis as one fresh Agent.Execute
// call, parses its JSON findings envelope, and maps it onto review.Result.
//
// review.Request has no WorkspacePath field (only Diff, Issue, Repository,
// and GateResults; see internal/review.Request's doc comment) — Review
// passes req's fields straight through to AgentRequest and leaves
// AgentRequest.WorkspacePath empty, since the axis's review context is the
// diff text itself rather than anything requiring filesystem access to the
// Worker's Workspace.
func (r *Reviewer) Review(ctx context.Context, req review.Request) (review.Result, error) {
	floor := r.ConfidenceFloor
	if floor <= 0 {
		floor = defaultConfidenceFloor
	}

	result, err := r.Agent.Execute(ctx, agent.AgentRequest{
		Issue:      req.Issue,
		Repository: req.Repository,
		Policy:     agent.WorkflowPolicy{Notes: buildPolicyNotes(req)},
	})
	if err != nil {
		return review.Result{}, fmt.Errorf("agentreviewer: axis %s: execute: %w", axisName, err)
	}

	env, err := parseEnvelope(result.Summary)
	if err != nil {
		return review.Result{}, fmt.Errorf("agentreviewer: axis %s: %w", axisName, err)
	}

	return buildResult(env, floor), nil
}
