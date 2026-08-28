// Package agent defines Forge's backend-independent contract for invoking a
// coding Agent (see CONTEXT.md "Agent"). It knows nothing about any
// particular backend (Claude Code, Codex): this package has zero dependency
// on backend SDKs or network libraries. Backend-specific translation lives
// in Agent Adapters that implement the Agent interface elsewhere.
package agent

import (
	"context"

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
}

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

	// AgentInstructions is repository-level guidance for the Agent (e.g.
	// CLAUDE.md contents or equivalent).
	AgentInstructions string

	// QualityGates lists the Quality Gate commands the implementation must
	// satisfy before publication (see CONTEXT.md "Quality Gate").
	QualityGates []string
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

// AgentResult is the structured outcome of one Agent.Execute call.
type AgentResult struct {
	// Status is one of StatusImplemented, StatusNeedsInfo, or StatusFailed.
	Status AgentStatus

	// Summary is a human-readable description of what the Agent did or why
	// it could not proceed.
	Summary string

	// NeedsInfo is populated only when Status is StatusNeedsInfo.
	NeedsInfo *NeedsInfoDetail
}
