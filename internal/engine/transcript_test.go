package engine_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

// transcriptEmittingAgent is an engine.Agent double that emits a scripted
// sequence of TranscriptEvents to req.Transcript (when set) before
// returning a programmed AgentResult — standing in for a real Agent
// Adapter (e.g. internal/agent/claude) that streams events as it works.
type transcriptEmittingAgent struct {
	result agent.AgentResult
}

func (a *transcriptEmittingAgent) Execute(_ context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	if req.Transcript != nil {
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "Looking at the issue."})
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolCall, Role: "assistant", ToolName: "Bash", ToolInput: `{"command":"go build ./..."}`})
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolResult, Role: "user", ToolName: "tool-1", ToolOutput: "build ok"})
	}
	return a.result, nil
}

var _ agent.Agent = (*transcriptEmittingAgent)(nil)

// TestExecute_PersistsAgentTranscriptEvents is ticket 28's end-to-end
// check: whatever an Agent emits to AgentRequest.Transcript during Execute
// is durably persisted, keyed to the AgentRun it was captured during, and
// readable back afterward via engine.LoadTranscript — the same read
// surface `forge status --transcript` uses.
func TestExecute_PersistsAgentTranscriptEvents(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: map[string]domain.Issue{"42": {ID: "42"}}}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := &transcriptEmittingAgent{result: agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"}}
	eng := engine.New(store, trk, &spyWorkspaces{mgr: mgr}, fake, config.Default(), repoRoot)

	ctx := context.Background()
	result, err := eng.Execute(ctx, "42", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	runs, err := store.AgentRunsByIssue(ctx, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d agent runs, want 1", len(runs))
	}

	transcript, err := engine.LoadTranscript(ctx, store, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(transcript) != 3 {
		t.Fatalf("got %d transcript events, want 3: %+v", len(transcript), transcript)
	}
	if transcript[0].Type != "MESSAGE" || transcript[0].Text != "Looking at the issue." {
		t.Fatalf("transcript[0] = %+v", transcript[0])
	}
	if transcript[1].Type != "TOOL_CALL" || transcript[1].ToolName != "Bash" {
		t.Fatalf("transcript[1] = %+v", transcript[1])
	}
	if transcript[2].Type != "TOOL_RESULT" || transcript[2].ToolOutput != "build ok" {
		t.Fatalf("transcript[2] = %+v", transcript[2])
	}
	for i, event := range transcript {
		if event.Seq != i {
			t.Fatalf("transcript[%d].Seq = %d, want %d", i, event.Seq, i)
		}
	}
}

// TestExecute_TranscriptPersistenceFailureDoesNotFailTheRun is ticket 28's
// best-effort requirement at the engine boundary: even if transcript
// storage were unavailable, an Issue's run must still complete normally.
// RecordTranscriptEvents is a no-op for an empty transcript, so an Agent
// that never emits anything (any Agent written before this ticket,
// including FakeAgent) must not be affected at all.
func TestExecute_TranscriptPersistenceFailureDoesNotFailTheRun(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"42": {ID: "42"}})
	te.fake.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateReviewing {
		t.Fatalf("final state = %s, want REVIEWING", result.Issue.State)
	}

	transcript, err := engine.LoadTranscript(ctx, te.store, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(transcript) != 0 {
		t.Fatalf("got %d transcript events for an Agent that emits none, want 0", len(transcript))
	}
}
