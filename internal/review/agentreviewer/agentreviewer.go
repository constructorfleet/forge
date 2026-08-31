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
// combine itself delegates to synthesizeFindings (synthesizer.go), issue
// #160's deterministic synthesizer: cross-axis dedup, confidence-fold,
// ranking, and tension detection over the merged set, all a pure function
// of the three axes' outcomes.
//
// Issue #161 makes this robust to a failing axis: each axis's own Execute
// call or envelope parse is retried, in place, up to axisMaxAttempts times
// (see its doc comment) before that axis is considered unrecoverable — this
// in-place retry is entirely internal to one Review call and never touches
// the engine's RetryBudget.Review counter (ADR-0007), which only tracks
// CHANGES_REQUIRED repairs. Review no longer returns an axis error to its
// caller (a change from #158's original all-or-nothing behavior); instead
// it degrades to a coverage-honest Result via combineDegraded: a surviving
// blocker still forces review.VerdictChangesRequired, but survivors-clean
// with at least one unrecoverable axis returns review.VerdictInconclusive
// rather than ever approving on partial coverage. Every Result also carries
// a Coverage record of which axes ran and which did not. Full per-axis
// audit persistence across runs (#162) is a separate, later ticket.
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

// axisMaxAttempts bounds one axis's own in-place retry (issue #161): a
// total of axisMaxAttempts Agent.Execute+parse attempts (the initial
// attempt plus axisMaxAttempts-1 retries) before that axis is treated as
// unrecoverable for this Review call. Deliberately small and fixed (not
// configurable) — this only exists to absorb a transient infra flake
// (a crash, an idle-timeout, an agent that forgot to emit JSON once), not to
// paper over a persistently broken axis, and it is entirely internal to one
// Reviewer.Review call: it never consumes or even inspects the engine's
// RetryBudget.Review counter, which is reserved for CHANGES_REQUIRED
// repairs (ADR-0007).
const axisMaxAttempts = 2

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
// call (retried in place per axis, see axisMaxAttempts), parses each
// surviving axis's JSON findings envelope, and folds them into one
// review.Result: combine for full coverage (every axis recovered),
// combineDegraded when at least one axis is unrecoverable.
//
// req.WorkspacePath is passed straight through to every axis's
// AgentRequest.WorkspacePath so each axis agent runs in, and can read, the
// same working tree Quality Gates ran against — letting it open files
// beyond the diff to trace cross-file/cross-package effects, rather than
// being confined to the diff text itself.
//
// Review itself never returns a non-nil error for an axis failure (a change
// from #158's original all-or-nothing behavior, see the package doc
// comment): every axis outcome, success or not, is folded into Result via
// Coverage instead.
func (r *Reviewer) Review(ctx context.Context, req review.Request) (review.Result, error) {
	floor := r.ConfidenceFloor
	if floor <= 0 {
		floor = defaultConfidenceFloor
	}

	outcomes := make([]axisOutcome, len(axes))
	axisErrs := make([]error, len(axes))
	unrecoverable := make([]bool, len(axes))

	var wg sync.WaitGroup
	wg.Add(len(axes))
	for i, ax := range axes {
		go func(i int, ax axis) {
			defer wg.Done()
			env, err := r.runAxisWithRetry(ctx, req, ax)
			if err != nil {
				unrecoverable[i] = true
				axisErrs[i] = err
				return
			}
			outcomes[i] = axisOutcome{axis: ax.name, env: env}
		}(i, ax)
	}
	wg.Wait()

	coverage := make([]review.AxisCoverage, len(axes))
	survivors := make([]axisOutcome, 0, len(axes))
	var failedAxes []string
	for i, ax := range axes {
		if unrecoverable[i] {
			coverage[i] = review.AxisCoverage{Axis: ax.name, Ran: false, Reason: axisErrs[i].Error()}
			failedAxes = append(failedAxes, ax.name)
			continue
		}
		coverage[i] = review.AxisCoverage{Axis: ax.name, Ran: true}
		survivors = append(survivors, outcomes[i])
	}

	var result review.Result
	if len(failedAxes) == 0 {
		result = combine(survivors, floor)
	} else {
		result = combineDegraded(survivors, failedAxes, floor)
	}
	result.Coverage = coverage

	return result, nil
}

// runAxisWithRetry runs one axis via runAxis, retrying in place up to
// axisMaxAttempts times (issue #161) when Execute errors or the envelope
// fails to parse. It returns the first successful envelope, or the last
// attempt's error once axisMaxAttempts is exhausted. Each goroutine Review
// launches calls this with its own axis and writes only to its own index of
// the caller's slices, so no additional synchronization is needed beyond
// the sync.WaitGroup Review already waits on.
func (r *Reviewer) runAxisWithRetry(ctx context.Context, req review.Request, ax axis) (envelope, error) {
	var lastErr error
	for attempt := 1; attempt <= axisMaxAttempts; attempt++ {
		env, err := r.runAxis(ctx, req, ax)
		if err == nil {
			return env, nil
		}
		lastErr = err
	}
	return envelope{}, fmt.Errorf("agentreviewer: axis %s: unrecoverable after %d attempt(s): %w", ax.name, axisMaxAttempts, lastErr)
}

// runAxis runs one axis as a single fresh Agent.Execute call and parses its
// JSON findings envelope.
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
