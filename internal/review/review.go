// Package review defines Forge's Review contract (CONTEXT.md "Review"): a
// fresh Agent invocation, independent of any implementation conversation,
// that evaluates a diff against an Issue's requirements and the Repository
// Context after Quality Gates pass. It knows nothing about how the engine's
// REVIEWING stage invokes it or how findings get routed back to the
// implementation Worker — see internal/engine for that wiring.
package review

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
)

// Reviewer performs one independent Review. Production adapters (a thin
// wrapper over a fresh agent.Agent invocation) and the deterministic
// FakeReviewer both satisfy this interface; internal/engine depends only on
// it, never on a concrete implementation.
type Reviewer interface {
	Review(ctx context.Context, req Request) (Result, error)
}

// Request carries everything a Reviewer needs to evaluate one Issue's
// implementation: the diff produced since the Worker's base revision, the
// Issue itself, the Repository Context shared across the Execution, and the
// Quality Gate results that passed before Review was invoked. It has no
// field for implementation conversation history — CONTEXT.md "Review" is
// explicit that the Reviewer never receives the implementation Agent's
// prior conversation, and there is no such object anywhere in this
// codebase for engine to pass even accidentally.
type Request struct {
	// Diff is the Workspace's diff (base...HEAD), produced by an injected
	// seam (internal/engine.DiffProducer) — Review itself has no git
	// dependency.
	Diff string

	// Issue is the normalized Issue under review (CONTEXT.md "Issue").
	Issue domain.Issue

	// Repository is the Repository Context compiled once per Execution
	// (CONTEXT.md "Repository Context").
	Repository agent.RepositoryContext

	// GateResults are the Quality Gate results that passed before Review
	// was invoked (CONTEXT.md "Quality Gate", "Gate Runner").
	GateResults []gate.Result

	// WorkspacePath is the execution's working tree — the same one Quality
	// Gates ran against (CONTEXT.md "Workspace") — so a Reviewer can open
	// files beyond the diff to trace cross-file/cross-package effects. It
	// carries no implementation conversation history; it is read-only
	// filesystem access alongside Diff, not a resumption of the
	// implementation Agent's prior invocation.
	WorkspacePath string
}

// Verdict is a Reviewer's outcome for one Review.
type Verdict string

const (
	// VerdictApproved means the implementation may proceed to COMMITTING.
	VerdictApproved Verdict = "APPROVED"

	// VerdictChangesRequired means the implementation must return to
	// IMPLEMENTING with Result.Findings routed back as agent.Feedback.
	VerdictChangesRequired Verdict = "CHANGES_REQUIRED"

	// VerdictInconclusive means the Review could not be completed on full
	// axis coverage (issue #161): at least one review axis was
	// unrecoverable (a crash, an idle-timeout, or a persistently
	// unparseable envelope) after the Reviewer's own bounded in-place
	// retries, and none of the surviving axes produced a blocking finding
	// to route back as CHANGES_REQUIRED instead. A false APPROVED is the
	// worst outcome Forge can produce, so an incomplete Review is never
	// silently approved — it is never FAILED either, since nothing here is
	// the implementation's fault. The engine routes VerdictInconclusive to
	// NEEDS_INFO (the same human-escalation resting state used elsewhere)
	// so a human, not an automated repair attempt, resolves the gap.
	VerdictInconclusive Verdict = "INCONCLUSIVE"
)

// Severity is how serious one Finding is.
type Severity string

const (
	SeverityInfo    Severity = "INFO"
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

// AxisSeverity is the severity vocabulary used by an individual review axis
// (bugs/security, code-quality, documentation) before it is folded onto
// Forge's Severity enum via MapAxisSeverity. Axes speak HIGH/MED/LOW
// natively; Severity is the coarser INFO/WARNING/ERROR vocabulary the rest
// of Forge (gates, feedback, engine) already understands.
type AxisSeverity string

const (
	AxisSeverityHigh AxisSeverity = "HIGH"
	AxisSeverityMed  AxisSeverity = "MED"
	AxisSeverityLow  AxisSeverity = "LOW"
)

// MapAxisSeverity maps one axis's HIGH/MED/LOW severity onto Forge's
// existing three-value Severity enum: HIGH becomes SeverityError, MED
// becomes SeverityWarning, and LOW (or anything unrecognized) becomes
// SeverityInfo. This is the single place that translation happens, so the
// synthesis step (a later ticket) never has to reimplement it.
func MapAxisSeverity(axisSeverity AxisSeverity) Severity {
	switch axisSeverity {
	case AxisSeverityHigh:
		return SeverityError
	case AxisSeverityMed:
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

// Finding is one structured issue a Reviewer raised against the diff, per
// issue 20's acceptance criteria (severity, file, line, message) as grown by
// issue 157 for the Thermo-style multi-axis Reviewer. File/Line are
// empty/zero for a Finding that isn't anchored to a specific location.
type Finding struct {
	Severity Severity
	File     string
	Line     int
	Message  string

	// Confidence is the merged confidence, 0.0-1.0, that this Finding is a
	// real, actionable issue.
	Confidence float64

	// Axis is which review axis surfaced this Finding: "bugs", "quality",
	// or "docs".
	Axis string

	// Remedy is the smallest correct change that would resolve this
	// Finding, so the implementation Worker knows exactly what to do
	// rather than just what's wrong.
	Remedy string

	// AgreedBy is how many axes independently surfaced this Finding;
	// higher agreement is stronger signal it's real.
	AgreedBy int
}

// Result is the structured outcome of one Reviewer.Review call.
type Result struct {
	// Verdict is VerdictApproved, VerdictChangesRequired, or
	// VerdictInconclusive.
	Verdict Verdict

	// Summary is a human-readable description of the Reviewer's overall
	// assessment.
	Summary string

	// Findings is populated when Verdict is VerdictChangesRequired. A
	// Reviewer may also populate it on VerdictApproved to surface
	// lower-severity or below-confidence-floor findings as advisory signal
	// (issue #158) — only runReview's repair-loop caller treats a
	// VerdictApproved Result's Findings as non-actionable, discarding them
	// rather than routing them back as agent.Feedback. On
	// VerdictInconclusive, Findings carries whatever advisory signal the
	// surviving axes produced (never a blocker — a surviving blocker forces
	// VerdictChangesRequired instead).
	Findings []Finding

	// Coverage records which axes a Reviewer actually ran to completion and
	// which it could not, and why (issue #161). It is nil for a Reviewer
	// that does not track per-axis coverage (e.g. FakeReviewer); the
	// production agentreviewer.Reviewer always populates it. Full per-axis
	// audit persistence across Review runs is issue #162 — this is
	// deliberately just enough structure for one Result's caller (runReview,
	// tests) to see the coverage picture without parsing Summary text.
	Coverage []AxisCoverage

	// Envelopes carries each axis that ran to completion's raw findings
	// envelope, exactly as that axis's agent emitted it, plus its token
	// usage when the backend exposed one — captured before synthesis
	// deduped/folded them into Findings (issue #162's full per-axis audit
	// trail). Only axes with a corresponding AxisCoverage.Ran == true entry
	// appear here; a Reviewer that does not track raw envelopes (e.g.
	// FakeReviewer) leaves this nil, same as Coverage.
	Envelopes []AxisEnvelope
}

// AxisRawFinding is one Finding exactly as a review axis's agent emitted it
// in its JSON findings envelope, before MapAxisSeverity or synthesis fold
// multiple axes' findings together. Persisted verbatim (issue #162) so a
// past Review's per-axis detail — including a Finding synthesis later
// dropped or merged away — can be reconstructed.
type AxisRawFinding struct {
	Severity   string
	Confidence float64
	File       string
	Line       int
	Message    string
	Evidence   string
	Remedy     string
}

// AxisEnvelope is one review axis's raw parsed findings envelope plus its
// token usage, captured before synthesis (issue #162).
type AxisEnvelope struct {
	// Axis is the axis name ("bugs", "quality", "docs").
	Axis string

	// Findings is this axis's raw findings, exactly as its agent emitted
	// them.
	Findings []AxisRawFinding

	// Usage is the axis agent invocation's token accounting, when its
	// backend exposed one (mirrors agent.AgentResult.Usage).
	Usage *agent.TokenUsage
}

// AxisCoverage is one review axis's outcome within a single Review call:
// whether it completed within the Reviewer's own bounded in-place retries,
// and — when it did not — why.
type AxisCoverage struct {
	// Axis is the axis name ("bugs", "quality", "docs" for
	// agentreviewer.Reviewer).
	Axis string

	// Ran is true when this axis produced a usable findings envelope.
	Ran bool

	// Reason explains why Ran is false (e.g. the wrapped Execute/parse
	// error after exhausting in-place retries). Empty when Ran is true.
	Reason string
}
