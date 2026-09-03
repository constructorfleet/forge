package planningagent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
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
//
// When constructed with NewPersistingAgentBackend, every Invoke -- from any
// planning package, since they all reach the Agent through this one seam --
// also records an agent_runs row and streams its transcript into
// transcript_events (issue #248), the way internal/engine already does for
// execution and review agents.
type AgentBackend struct {
	agent agent.Agent

	// store, executionID and issueID are set only by
	// NewPersistingAgentBackend; a nil store means Invoke behaves exactly
	// as it did before transcript persistence existed (no agent_runs row,
	// nil req.Transcript, no writes at all).
	store                TranscriptStore
	executionID, issueID string

	// now is the clock the transcript recorder stamps events with,
	// overridable in tests. nil means time.Now.
	now func() time.Time
}

// NewAgentBackend returns an AgentBackend that runs every Invoke call
// through a, persisting nothing.
func NewAgentBackend(a agent.Agent) *AgentBackend {
	return &AgentBackend{agent: a}
}

// NewPersistingAgentBackend returns an AgentBackend that, in addition to
// running every Invoke call through a, records each invocation as an
// agent_runs row scoped to executionID/issueID and streams its transcript
// into transcript_events tagged phase=TranscriptPhase and
// subagent=InvokeRequest.Key.
//
// Persistence is best-effort throughout, matching internal/engine's
// contract: a storage failure is a durability gap for that invocation's
// telemetry, never a reason to fail the planning call.
func NewPersistingAgentBackend(a agent.Agent, store TranscriptStore, executionID, issueID string) *AgentBackend {
	return &AgentBackend{agent: a, store: store, executionID: executionID, issueID: issueID}
}

// TranscriptScope reports the execution/issue identifiers this backend
// records agent_runs and transcript_events under, and whether it persists
// them at all (false for NewAgentBackend, which writes nothing). It lets a
// caller that assembled the backend confirm what a planning transcript will
// be filed under without reaching into the wrapped Store.
func (b *AgentBackend) TranscriptScope() (executionID, issueID string, persisting bool) {
	return b.executionID, b.issueID, b.store != nil
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
//
// On a NewPersistingAgentBackend, the call is additionally bracketed by
// StartAgentRun/FinalizeAgentRun and its transcript streamed into
// transcript_events under subagent=req.Key -- so every planning package
// that reaches an Agent through this seam is recorded without knowing
// anything about storage.
func (b *AgentBackend) Invoke(ctx context.Context, req InvokeRequest) (string, error) {
	workDir, err := os.MkdirTemp("", "forge-planning-*")
	if err != nil {
		return "", fmt.Errorf("planningagent: create isolated working directory: %w", err)
	}

	agentReq := agent.AgentRequest{
		WorkspacePath: workDir,
		Issue:         domain.Issue{ID: req.Key},
		Mode:          agent.ModeStructured,
		Prompt:        req.Prompt,
		Schema:        string(req.Schema),
	}

	agentRunID, started, persisting := b.startRun(ctx, agentReq)
	if persisting {
		agentReq.Transcript = newPersistingTranscriptSink(ctx, b.store, b.executionID, b.issueID, agentRunID, req.Key, b.now)
	} else if b.store != nil {
		// StartAgentRun failed: there is no agent_runs row to key a
		// transcript to, so capture degrades to a discard rather than
		// leaving req.Transcript nil mid-flight.
		agentReq.Transcript = noopTranscriptSink{}
	}

	result, execErr := b.agent.Execute(ctx, agentReq)
	if sink, ok := agentReq.Transcript.(*persistingTranscriptSink); ok {
		sink.Close()
	}
	if persisting {
		b.finalizeRun(ctx, agentRunID, started, result, execErr)
	}
	if execErr != nil {
		return "", fmt.Errorf("planningagent: execute %s: %w", req.Key, execErr)
	}

	return result.Summary, nil
}

// startRun inserts the in-progress agent_runs row up front (as
// internal/engine does) so the transcript sink has an agent_run_id to
// persist against from the very first event, rather than only once Execute
// returns. Reports persisting=false -- and the caller then skips both the
// sink and finalization -- when this backend has no store, or when the
// insert failed.
func (b *AgentBackend) startRun(ctx context.Context, agentReq agent.AgentRequest) (agentRunID int64, started time.Time, persisting bool) {
	if b.store == nil {
		return 0, time.Time{}, false
	}
	started = b.clock()()
	// A telemetry-only figure: an unencodable request costs the run its
	// context-size metric, not its transcript.
	contextBytes, _ := agent.ContextSizeBytes(agentReq)
	agentRunID, err := b.store.StartAgentRun(ctx, storage.AgentRun{
		ExecutionID:  b.executionID,
		IssueID:      b.issueID,
		Backend:      TranscriptBackendName,
		StartedAt:    started,
		ContextBytes: contextBytes,
	})
	if err != nil {
		return 0, started, false
	}
	return agentRunID, started, true
}

// finalizeRun closes out the agent_runs row with its terminal result and
// token usage. Transcript events were already persisted incrementally as
// they were emitted, so a run whose process dies before this call keeps its
// transcript and is left at AgentRunResultRunning -- a durable "interrupted"
// marker -- rather than losing the row.
func (b *AgentBackend) finalizeRun(ctx context.Context, agentRunID int64, started time.Time, result agent.AgentResult, execErr error) {
	run := storage.AgentRun{
		ExecutionID: b.executionID,
		IssueID:     b.issueID,
		Backend:     TranscriptBackendName,
		StartedAt:   started,
		FinishedAt:  b.clock()(),
		Result:      string(result.Status),
	}
	if run.Result == "" && execErr != nil {
		run.Result = "ERROR"
	}
	if result.Usage != nil {
		inputTokens := result.Usage.InputTokens
		outputTokens := result.Usage.OutputTokens
		run.InputTokens = &inputTokens
		run.OutputTokens = &outputTokens
	}
	_ = b.store.FinalizeAgentRun(ctx, agentRunID, run)
}

func (b *AgentBackend) clock() func() time.Time {
	if b.now == nil {
		return time.Now
	}
	return b.now
}
