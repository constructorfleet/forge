// Package agent defines Forge's backend-independent contract for invoking a
// coding Agent (see CONTEXT.md "Agent"). It knows nothing about any
// particular backend (Claude Code, Codex): this package has zero dependency
// on backend SDKs or network libraries. Backend-specific translation lives
// in Agent Adapters that implement the Agent interface elsewhere.
package agent

import (
	"context"
	"encoding/json"

	"github.com/Teagan42/forge/internal/domain"
)

// Agent is the external coding backend invoked by a Worker for one Issue.
// It receives normalized context and returns a structured result; it does
// not perform workflow mechanics (see CONTEXT.md "Agent").
type Agent interface {
	Execute(ctx context.Context, req AgentRequest) (AgentResult, error)
}

// AgentRequest carries everything an Agent needs to attempt (or continue
// attempting) an Issue: where to work, what the Issue asks for, the
// Repository Context shared across the Execution, the workflow policy in
// effect, and any Feedback accumulated from prior repair iterations.
type AgentRequest struct {
	// WorkspacePath is the filesystem location of the Issue's Workspace
	// (see CONTEXT.md "Workspace") where the Agent should make changes.
	WorkspacePath string

	// Issue is the normalized Issue the Agent is working on.
	Issue domain.Issue

	// Repository is the Repository Context: information shared across all
	// Workers in the Execution (quality gates, project structure, agent
	// instructions, base revision). Compiled by a later ticket (17); this
	// package only depends on its shape.
	Repository RepositoryContext

	// Policy is the workflow policy in effect for this Worker.
	Policy WorkflowPolicy

	// Feedback carries bounded diagnostics from prior gate failures, review
	// rejections, or CI failures, so a repair iteration can address them.
	// Empty on the first attempt.
	Feedback []Feedback

	// Transcript, if non-nil, receives TranscriptEvents (messages, tool
	// calls, tool results) as this invocation progresses (ticket 28).
	// Optional: nil means no capture, matching every Agent's behavior
	// before this field existed. Excluded from JSON so ContextSizeBytes
	// keeps measuring only the context handed to the Agent, not this
	// output-only seam.
	Transcript TranscriptSink `json:"-"`

	// Semantic is the backend-neutral Semantic Navigation descriptor a
	// SemanticProvider seam (internal/semantic) attaches via
	// Session.Augment before Execute is called. Zero value means no
	// semantic navigation, the pre-existing behavior for every Agent call
	// before this field existed.
	Semantic SemanticDescriptor

	// Mode selects how the backend Adapter frames this invocation. The zero
	// value (ModeImplement) is the pre-existing implementation task —
	// schema-enforced {status, summary} result envelope, implement/TDD
	// framing — for every call before this field existed. ModeReview is a
	// read-only analysis task whose deliverable is the agent's final message
	// itself (e.g. a review findings envelope the caller parses), not a code
	// change; a backend that honors it must not enforce the implement-mode
	// result envelope on that output. ModeStructured is a structured
	// invocation: the backend uses Prompt verbatim, enforces Schema, and
	// returns the schema-conforming result as AgentResult.Summary. A backend
	// that does not recognize Mode simply runs its default implement-mode
	// behavior.
	Mode AgentMode

	// Prompt is a verbatim prompt for a structured invocation (ModeStructured
	// only). Empty for every other Mode, preserving existing behavior.
	Prompt string

	// Schema is a per-call JSON schema string the backend must enforce on its
	// result for a structured invocation (ModeStructured only). Empty for
	// every other Mode, preserving existing behavior.
	Schema string
}

// AgentMode selects how a backend Adapter frames an Execute invocation (see
// AgentRequest.Mode).
type AgentMode string

const (
	// ModeImplement is the default: an implementation task whose outcome is
	// reported through the backend's structured {status, summary} result
	// envelope. The zero value, so every AgentRequest built before Mode
	// existed keeps this behavior.
	ModeImplement AgentMode = ""

	// ModeReview is a read-only analysis task (e.g. one review axis): the
	// agent's final message is the deliverable itself, returned verbatim as
	// AgentResult.Summary for the caller to parse, with none of implement
	// mode's result-envelope enforcement or implement/TDD prompt framing.
	ModeReview AgentMode = "REVIEW"

	// ModeStructured is a structured invocation: the backend uses
	// AgentRequest.Prompt verbatim and enforces AgentRequest.Schema, then
	// returns the schema-conforming result as AgentResult.Summary — the same
	// deliverable-as-final-message convention ModeReview uses.
	ModeStructured AgentMode = "STRUCTURED"
)

// RepositoryContext is relatively stable information shared across all
// Workers in an Execution, compiled once per Execution (see CONTEXT.md
// "Repository Context"). This is a minimal, forward-compatible shape;
// ticket 17 (Repository Context compiler) owns its full population.
type RepositoryContext struct {
	// BaseRevision is the base revision the Execution started from.
	BaseRevision string

	// ProjectStructure is a deterministic description of the repository
	// layout supplied to the Agent (e.g. directory tree, key files).
	ProjectStructure string

	// AgentInstructions is repository-level guidance for the Agent,
	// normalized and merged from AGENTS.md and CLAUDE.md when present (see
	// ticket 17, the Repository Context compiler).
	AgentInstructions string

	// QualityGates lists the Quality Gate commands the implementation must
	// satisfy before publication (see CONTEXT.md "Quality Gate"). These are
	// sourced exclusively from configuration (config.Config); Workers never
	// rediscover them by scanning the repository.
	QualityGates []string

	// Languages lists the programming languages detected in the repository
	// from project manifests (e.g. go.mod -> Go). Informational only: it
	// describes the repository for the Agent's benefit and never determines
	// QualityGates, which are configured, not detected.
	Languages []string

	// PackageManagers lists the package managers detected in the repository
	// from project manifests (e.g. go.mod -> Go Modules). Informational
	// only, for the same reason as Languages.
	PackageManagers []string
}

// WorkflowPolicy is the set of workflow rules in effect for a Worker. This
// is a minimal, forward-compatible shape; later tickets extend it as
// specific policies (retry ceilings, review requirements, etc.) are wired
// in.
type WorkflowPolicy struct {
	// Notes carries free-form policy guidance until dedicated fields exist.
	Notes string
}

// FeedbackSource identifies where a piece of repair Feedback originated.
type FeedbackSource string

const (
	FeedbackSourceGate   FeedbackSource = "GATE"
	FeedbackSourceReview FeedbackSource = "REVIEW"
	FeedbackSourceCI     FeedbackSource = "CI"
)

// Feedback is one bounded diagnostic routed back to the Agent for a repair
// iteration (see CONTEXT.md "Gate Runner", "Review", "CI Supervisor").
type Feedback struct {
	Source  FeedbackSource
	Message string
}

// AgentStatus is the outcome of an Agent's attempt at an Issue.
type AgentStatus string

const (
	// StatusImplemented means the Agent completed the Issue's work.
	StatusImplemented AgentStatus = "IMPLEMENTED"

	// StatusNeedsInfo means the Agent could not proceed without human
	// input; AgentResult.NeedsInfo describes what is needed.
	StatusNeedsInfo AgentStatus = "NEEDS_INFO"

	// StatusFailed means the Agent could not complete the Issue and no
	// further automated progress is possible for this attempt.
	StatusFailed AgentStatus = "FAILED"

	// StatusReplanRequired means the Agent discovered that the plan
	// governing this Issue is itself invalid — not that the Issue is hard,
	// and not that a human needs to answer a question about it (that is
	// StatusNeedsInfo). AgentResult.Replan describes what was discovered.
	// This is a structural escalation: Forge freezes the Feature and
	// reopens the planning decision rather than repairing the Issue.
	StatusReplanRequired AgentStatus = "REPLAN_REQUIRED"
)

// NeedsInfoDetail describes, in structured form, what a StatusNeedsInfo
// result requires from a human before work can resume (see CONTEXT.md
// "Needs-info resume flow").
type NeedsInfoDetail struct {
	// Question is what the Agent needs answered.
	Question string

	// Context is supporting detail explaining why the question arose.
	Context string
}

// ReplanDetail describes, in structured form, why a StatusReplanRequired
// result considers the governing plan invalid. Mirrors NeedsInfoDetail's
// shape (a small, definitional payload the engine renders and persists
// verbatim) but carries the extra structure a planning Decision needs to be
// created or reopened from it.
type ReplanDetail struct {
	// Reason is the invalidity the Agent discovered, in one sentence.
	Reason string

	// Evidence is the concrete supporting detail — file paths, conflicting
	// requirements, observed behavior — that makes Reason checkable rather
	// than an assertion.
	Evidence string

	// AffectedRequirements lists the requirement IDs (as stamped on the
	// Issue's Forge Provenance block) the Agent believes the invalidity
	// reaches.
	AffectedRequirements []string

	// SuggestedQuestion is the planning question the Agent proposes the
	// reopened Decision should answer.
	SuggestedQuestion string
}

// TokenUsage is the optional token accounting an Agent backend may expose
// for one invocation.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// AgentResult is the structured outcome of one Agent.Execute call.
type AgentResult struct {
	// Status is one of StatusImplemented, StatusNeedsInfo, StatusFailed, or
	// StatusReplanRequired.
	Status AgentStatus

	// Summary is a human-readable description of what the Agent did or why
	// it could not proceed.
	Summary string

	// NeedsInfo is populated only when Status is StatusNeedsInfo.
	NeedsInfo *NeedsInfoDetail

	// Replan is populated only when Status is StatusReplanRequired.
	Replan *ReplanDetail

	// Usage is populated only when the backend exposes token accounting for
	// this invocation.
	Usage *TokenUsage
}

// ContextSizeBytes returns the byte size of req's normalized execution
// context as serialized for storage/telemetry. This measures the
// backend-independent context Forge assembled and handed to the Agent,
// rather than any backend-specific prompt wrapper layered on afterward.
func ContextSizeBytes(req AgentRequest) (int, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}
