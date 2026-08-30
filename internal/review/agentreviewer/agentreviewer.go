// Package agentreviewer is Forge's production review.Reviewer (issues #158,
// #159): it runs THREE review axes — bugs/breaking/security, code-quality/
// maintainability, and documentation — as concurrent, independent
// agent.Agent invocations per Review call (ADR-0004: no implementation
// conversation — see internal/review's package doc), each axis's rubric
// embedded in the binary via go:embed and injected through
// agent.AgentRequest.Policy.Notes (the only free-form guidance primitive
// AgentRequest exposes).
//
// Each axis agent is instructed to emit a review-shaped JSON findings
// envelope as its final output; since agent.AgentResult carries no
// dedicated findings field (Summary is its only text surface), Reviewer
// parses that envelope out of AgentResult.Summary (see envelope.go) for
// every axis and folds all three axes' findings into one review.Result (see
// combine in verdict.go): a Finding that maps to review.SeverityError with
// Confidence at or above ConfidenceFloor forces
// review.VerdictChangesRequired; otherwise the Review is
// review.VerdictApproved, with any lower-severity or below-floor findings
// still attached to Result.Findings as advisory signal.
//
// Issue #159's combine is deliberately a simple concatenation of the three
// axes' findings — no cross-axis dedup, confidence-fold, ranking, or
// tension detection. That is the deterministic synthesizer, a separate
// later ticket (#160). Degradation handling for one axis's malformed
// envelope or Execute error (#161) and full audit persistence (#162) are
// also separate, later tickets — this package deliberately does none of
// that: an axis error still propagates as Review's error, per #158's
// original behavior.
package agentreviewer

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/review"
)

//go:embed rubric.md
var bugsRubric string

//go:embed quality_rubric.md
var qualityRubric string

//go:embed docs_rubric.md
var docsRubric string

// defaultConfidenceFloor mirrors config.Default's
// Workflow.ReviewConfidenceFloor default, so a Reviewer constructed with
// confidenceFloor <= 0 (e.g. directly in a test, bypassing config) still
// behaves sensibly rather than blocking on every ERROR-severity finding
// regardless of confidence.
const defaultConfidenceFloor = 0.7

// var declaration ensures Reviewer satisfies review.Reviewer at compile
// time.
var _ review.Reviewer = (*Reviewer)(nil)

// Reviewer is the production three-axis (bugs/breaking/security,
// code-quality, documentation) review.Reviewer. It holds the agent.Agent
// used to run each axis and the confidence floor gating the combined
// verdict.
type Reviewer struct {
	// Agent runs each axis as a fresh, independent invocation per Review
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

// axis is one review axis this Reviewer runs: a name (as stamped onto every
// Finding it produces) paired with its embedded rubric text.
type axis struct {
	name   string
	rubric string
}

// axes is the fixed, ordered set of axes Review fans out to (issue #159: N
// is fixed at 3, no dynamic axis registration). Findings in a combined
// Result are always concatenated in this order — bugs, then quality, then
// docs — regardless of which axis's goroutine finishes first, so combine's
// output is deterministic even though the underlying Agent.Execute calls
// race.
var axes = []axis{
	{name: "bugs", rubric: bugsRubric},
	{name: "quality", rubric: qualityRubric},
	{name: "docs", rubric: docsRubric},
}

// Review runs all three axes concurrently, each as one fresh Agent.Execute
// call, parses each axis's JSON findings envelope, and folds them into one
// review.Result via combine.
//
// req.WorkspacePath is passed straight through to every axis's
// AgentRequest.WorkspacePath so each axis agent runs in, and can read, the
// same working tree Quality Gates ran against — letting it open files
// beyond the diff to trace cross-file/cross-package effects, rather than
// being confined to the diff text itself.
//
// If any axis's Execute call or envelope parse fails, Review returns that
// error (the first one observed) rather than a partial Result — per #158's
// original error behavior, unchanged here; tolerating one axis's failure
// while still combining the other two is #161's degradation handling, a
// separate later ticket.
func (r *Reviewer) Review(ctx context.Context, req review.Request) (review.Result, error) {
	floor := r.ConfidenceFloor
	if floor <= 0 {
		floor = defaultConfidenceFloor
	}

	outcomes := make([]axisOutcome, len(axes))
	errs := make([]error, len(axes))

	var wg sync.WaitGroup
	wg.Add(len(axes))
	for i, ax := range axes {
		go func(i int, ax axis) {
			defer wg.Done()
			env, err := r.runAxis(ctx, req, ax)
			if err != nil {
				errs[i] = err
				return
			}
			outcomes[i] = axisOutcome{axis: ax.name, env: env}
		}(i, ax)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return review.Result{}, err
		}
	}

	return combine(outcomes, floor), nil
}

// runAxis runs one axis as a single fresh Agent.Execute call and parses its
// JSON findings envelope. Each goroutine Review launches calls this with
// its own axis and writes only to its own index of the caller's outcomes/
// errs slices, so no additional synchronization is needed beyond the
// sync.WaitGroup Review already waits on.
func (r *Reviewer) runAxis(ctx context.Context, req review.Request, ax axis) (envelope, error) {
	result, err := r.Agent.Execute(ctx, agent.AgentRequest{
		Issue:         req.Issue,
		Repository:    req.Repository,
		Policy:        agent.WorkflowPolicy{Notes: buildPolicyNotes(req, ax.rubric)},
		WorkspacePath: req.WorkspacePath,
	})
	if err != nil {
		return envelope{}, fmt.Errorf("agentreviewer: axis %s: execute: %w", ax.name, err)
	}

	env, err := parseEnvelope(result.Summary)
	if err != nil {
		return envelope{}, fmt.Errorf("agentreviewer: axis %s: %w", ax.name, err)
	}

	return env, nil
}
