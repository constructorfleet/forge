package planningagent

import (
	"context"
	"fmt"
	"os"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

var _ Backend = (*AgentBackend)(nil)

// AgentBackend is the production Backend: it bridges Phase 2 planning
// contracts to any real agent.Agent (Claude today, others later) by framing
// each Invoke call as a ModeStructured AgentRequest -- the contract's Prompt
// verbatim, the per-call Schema -- and returning AgentResult.Summary (the
// schema-conforming result) as its raw string.
//
// Every invocation runs in a freshly created, empty temp directory rather
// than the repository root, so the wrapped Agent has no repository to act
// on: planning is pure text-in, text-out, with no repo tools available to
// it (see #197's "no repo tools" decision).
type AgentBackend struct {
	agent agent.Agent
}

// NewAgentBackend returns an AgentBackend that runs every Invoke call
// through a.
func NewAgentBackend(a agent.Agent) *AgentBackend {
	return &AgentBackend{agent: a}
}

// Invoke builds a ModeStructured agent.AgentRequest from req (verbatim
// Prompt, per-call Schema) and an isolated, empty temp directory as
// WorkspacePath, runs it through the wrapped agent.Agent, and returns
// AgentResult.Summary as the raw structured-result string InvokeStructured
// decodes. req.Key is threaded through as Issue.ID -- ModeStructured
// backends (see the Claude adapter's buildPrompt) ignore Issue entirely, so
// this is purely a scripting hook for test doubles like agent.FakeAgent,
// which key their programmed outcomes on it. A backend error from Execute
// surfaces as an Invoke error.
func (b *AgentBackend) Invoke(ctx context.Context, req InvokeRequest) (string, error) {
	workDir, err := os.MkdirTemp("", "forge-planning-*")
	if err != nil {
		return "", fmt.Errorf("planningagent: create isolated working directory: %w", err)
	}

	result, err := b.agent.Execute(ctx, agent.AgentRequest{
		WorkspacePath: workDir,
		Issue:         domain.Issue{ID: req.Key},
		Mode:          agent.ModeStructured,
		Prompt:        req.Prompt,
		Schema:        string(req.Schema),
	})
	if err != nil {
		return "", fmt.Errorf("planningagent: execute %s: %w", req.Key, err)
	}

	return result.Summary, nil
}
