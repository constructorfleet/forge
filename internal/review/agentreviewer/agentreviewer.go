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
	"os"
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

// RubricOverrides optionally substitutes a team's own rubric text for one
// or more axes (issue #162, config field
// workflow.review_rubrics.{bugs,quality,docs}), read from the paths that
// field names. A blank field leaves that axis's embedded default rubric
// (rubric.md/quality_rubric.md/docs_rubric.md) in place — this is a
// per-field override, not an all-or-nothing switch.
type RubricOverrides struct {
	Bugs    string
	Quality string
	Docs    string
}

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

	// Rubrics optionally overrides one or more axes' embedded rubric text
	// (issue #162). Zero value (RubricOverrides{}) uses every axis's
	// embedded default, unchanged from pre-#162 behavior.
	Rubrics RubricOverrides
}

// New returns a Reviewer running axis reviews via a, gating verdicts at
// confidenceFloor. confidenceFloor <= 0 uses defaultConfidenceFloor. Every
// axis uses its embedded default rubric; set the returned Reviewer's
// Rubrics field (or use LoadRubricOverrides) to override one or more axes.
func New(a agent.Agent, confidenceFloor float64) *Reviewer {
	if confidenceFloor <= 0 {
		confidenceFloor = defaultConfidenceFloor
	}
	return &Reviewer{Agent: a, ConfidenceFloor: confidenceFloor}
}

// RubricOverridePaths names, per axis, an optional file path whose contents
// should replace that axis's embedded default rubric (issue #162) — the
// same shape as config.WorkflowConfig.ReviewRubrics, without this package
// depending on internal/config (the caller, cmd/forge's wiring, translates
// between the two).
type RubricOverridePaths struct {
	Bugs    string
	Quality string
	Docs    string
}

// LoadRubricOverrides reads every non-blank path in paths and returns the
// resulting RubricOverrides with each axis's file contents in place of its
// path. A blank path is left blank (that axis keeps its embedded default).
// Returns an error naming the offending axis if a non-blank path can't be
// read — callers (cmd/forge's wiring) are expected to validate readability
// upfront via config.Load, so a failure here indicates the file changed or
// vanished between validation and startup.
func LoadRubricOverrides(paths RubricOverridePaths) (RubricOverrides, error) {
	var out RubricOverrides
	for _, f := range []struct {
		axis string
		path string
		dst  *string
	}{
		{"bugs", paths.Bugs, &out.Bugs},
		{"quality", paths.Quality, &out.Quality},
		{"docs", paths.Docs, &out.Docs},
	} {
		if f.path == "" {
			continue
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			return RubricOverrides{}, fmt.Errorf("agentreviewer: load %s rubric override %s: %w", f.axis, f.path, err)
		}
		*f.dst = string(data)
	}
	return out, nil
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

// effectiveAxes returns axes with any of r.Rubrics' non-blank per-axis
// overrides substituted in place of that axis's embedded default rubric
// (issue #162). Order and names are unchanged from axes; only rubric text
// may differ.
func (r *Reviewer) effectiveAxes() []axis {
	overrides := map[string]string{
		"bugs":    r.Rubrics.Bugs,
		"quality": r.Rubrics.Quality,
		"docs":    r.Rubrics.Docs,
	}
	eff := make([]axis, len(axes))
	for i, ax := range axes {
		eff[i] = ax
		if o := overrides[ax.name]; o != "" {
			eff[i].rubric = o
		}
	}
	return eff
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

	axesList := r.effectiveAxes()

	outcomes := make([]axisOutcome, len(axesList))
	usages := make([]*agent.TokenUsage, len(axesList))
	axisErrs := make([]error, len(axesList))
	unrecoverable := make([]bool, len(axesList))

	var wg sync.WaitGroup
	wg.Add(len(axesList))
	for i, ax := range axesList {
		go func(i int, ax axis) {
			defer wg.Done()
			env, usage, err := r.runAxisWithRetry(ctx, req, ax)
			if err != nil {
				unrecoverable[i] = true
				axisErrs[i] = err
				return
			}
			outcomes[i] = axisOutcome{axis: ax.name, env: env}
			usages[i] = usage
		}(i, ax)
	}
	wg.Wait()

	coverage := make([]review.AxisCoverage, len(axesList))
	survivors := make([]axisOutcome, 0, len(axesList))
	envelopes := make([]review.AxisEnvelope, 0, len(axesList))
	var failedAxes []string
	for i, ax := range axesList {
		if unrecoverable[i] {
			coverage[i] = review.AxisCoverage{Axis: ax.name, Ran: false, Reason: axisErrs[i].Error()}
			failedAxes = append(failedAxes, ax.name)
			continue
		}
		coverage[i] = review.AxisCoverage{Axis: ax.name, Ran: true}
		survivors = append(survivors, outcomes[i])
		envelopes = append(envelopes, review.AxisEnvelope{
			Axis:     ax.name,
			Findings: toRawFindings(outcomes[i].env),
			Usage:    usages[i],
		})
	}

	var result review.Result
	if len(failedAxes) == 0 {
		result = combine(survivors, floor)
	} else {
		result = combineDegraded(survivors, failedAxes, floor)
	}
	result.Coverage = coverage
	result.Envelopes = envelopes

	return result, nil
}

// toRawFindings maps one axis's parsed envelope onto
// []review.AxisRawFinding, exactly as that axis's agent emitted it — issue
// #162's raw, pre-synthesis audit record, as distinct from
// findingsForAxis's review.Finding mapping (which applies MapAxisSeverity
// and the verdict-blocking rule).
func toRawFindings(env envelope) []review.AxisRawFinding {
	out := make([]review.AxisRawFinding, len(env.Findings))
	for i, f := range env.Findings {
		out[i] = review.AxisRawFinding{
			Severity:   f.Severity,
			Confidence: f.Confidence,
			File:       f.File,
			Line:       f.Line,
			Message:    f.Message,
			Evidence:   f.Evidence,
			Remedy:     f.Remedy,
		}
	}
	return out
}

// runAxisWithRetry runs one axis via runAxis, retrying in place up to
// axisMaxAttempts times (issue #161) when Execute errors or the envelope
// fails to parse. It returns the first successful envelope and its token
// usage, or the last attempt's error once axisMaxAttempts is exhausted.
// Each goroutine Review launches calls this with its own axis and writes
// only to its own index of the caller's slices, so no additional
// synchronization is needed beyond the sync.WaitGroup Review already waits
// on.
func (r *Reviewer) runAxisWithRetry(ctx context.Context, req review.Request, ax axis) (envelope, *agent.TokenUsage, error) {
	var lastErr error
	for attempt := 1; attempt <= axisMaxAttempts; attempt++ {
		env, usage, err := r.runAxis(ctx, req, ax)
		if err == nil {
			return env, usage, nil
		}
		lastErr = err
	}
	return envelope{}, nil, fmt.Errorf("agentreviewer: axis %s: unrecoverable after %d attempt(s): %w", ax.name, axisMaxAttempts, lastErr)
}

// runAxis runs one axis as a single fresh Agent.Execute call and parses its
// JSON findings envelope, returning the AgentResult's token usage alongside
// it (issue #162) for Result.Envelopes.
func (r *Reviewer) runAxis(ctx context.Context, req review.Request, ax axis) (envelope, *agent.TokenUsage, error) {
	result, err := r.Agent.Execute(ctx, agent.AgentRequest{
		Issue:         req.Issue,
		Repository:    req.Repository,
		Policy:        agent.WorkflowPolicy{Notes: buildPolicyNotes(req, ax.rubric)},
		WorkspacePath: req.WorkspacePath,
	})
	if err != nil {
		return envelope{}, nil, fmt.Errorf("agentreviewer: axis %s: execute: %w", ax.name, err)
	}

	env, err := parseEnvelope(result.Summary)
	if err != nil {
		return envelope{}, nil, fmt.Errorf("agentreviewer: axis %s: %w", ax.name, err)
	}

	return env, result.Usage, nil
}
