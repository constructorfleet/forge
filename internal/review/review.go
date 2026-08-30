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
}

// Verdict is a Reviewer's outcome for one Review.
type Verdict string

const (
	// VerdictApproved means the implementation may proceed to COMMITTING.
	VerdictApproved Verdict = "APPROVED"

	// VerdictChangesRequired means the implementation must return to
	// IMPLEMENTING with Result.Findings routed back as agent.Feedback.
	VerdictChangesRequired Verdict = "CHANGES_REQUIRED"
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
	// Verdict is VerdictApproved or VerdictChangesRequired.
	Verdict Verdict

	// Summary is a human-readable description of the Reviewer's overall
	// assessment.
	Summary string

	// Findings is populated when Verdict is VerdictChangesRequired. A
	// Reviewer may also populate it on VerdictApproved to surface
	// lower-severity or below-confidence-floor findings as advisory signal
	// (issue #158) — only runReview's repair-loop caller treats a
	// VerdictApproved Result's Findings as non-actionable, discarding them
	// rather than routing them back as agent.Feedback.
	Findings []Finding
}
