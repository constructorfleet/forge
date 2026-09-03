package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/review"
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
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolCall, Role: "assistant", ToolName: "Bash", ToolCallID: "tool-1", ToolInput: `{"command":"go build ./..."}`})
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolResult, Role: "user", ToolName: "Bash", ToolCallID: "tool-1", ToolOutput: "build ok"})
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
	if transcript[1].ToolCallID != "tool-1" || transcript[2].ToolCallID != "tool-1" {
		t.Fatalf("transcript[1].ToolCallID = %q, transcript[2].ToolCallID = %q, want both tool-1 (call/result pairing survives persistence)", transcript[1].ToolCallID, transcript[2].ToolCallID)
	}
	for i, event := range transcript {
		if event.Phase != "IMPLEMENTING" {
			t.Fatalf("transcript[%d].Phase = %q, want IMPLEMENTING", i, event.Phase)
		}
		if event.Subagent != "" {
			t.Fatalf("transcript[%d].Subagent = %q, want empty (single implementation agent)", i, event.Subagent)
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
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	transcript, err := engine.LoadTranscript(ctx, te.store, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(transcript) != 0 {
		t.Fatalf("got %d transcript events for an Agent that emits none, want 0", len(transcript))
	}
}

// cancelableTranscriptAgent emits a scripted sequence of events, signals
// emitted once they've been sent to the sink, then blocks on ctx.Done() —
// standing in for an Agent whose subprocess is killed mid-run (issue 36:
// the case that motivated this ticket, a real timed-out run whose
// transcript was mostly lost to end-of-run batch persistence).
type cancelableTranscriptAgent struct {
	emitted chan struct{}
}

func (a *cancelableTranscriptAgent) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "starting work"})
	req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventToolCall, Role: "assistant", ToolName: "Bash", ToolCallID: "tool-1", ToolInput: `{"command":"go build ./..."}`})
	close(a.emitted)
	<-ctx.Done()
	return agent.AgentResult{}, ctx.Err()
}

var _ agent.Agent = (*cancelableTranscriptAgent)(nil)

// TestExecute_CancelledMidStream_RetainsTranscriptUpToCancellation is issue
// 36's core durability requirement: a killed/timed-out run's transcript must
// survive up to the moment of the kill. With batched, debounced append-only
// flushing (issue 489), the tail is guaranteed by the sink's final Close,
// which runs on a cancel-immune context when Execute returns — so a run
// cancelled mid-stream still persists everything it emitted before the wedge,
// rather than losing it to an end-of-run batch that never gets to run.
func TestExecute_CancelledMidStream_RetainsTranscriptUpToCancellation(t *testing.T) {
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: map[string]domain.Issue{"42": {ID: "42"}}}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := &cancelableTranscriptAgent{emitted: make(chan struct{})}
	eng := engine.New(store, trk, &spyWorkspaces{mgr: mgr}, fake, config.Default(), repoRoot)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	execution, err := eng.StartExecution(ctx, base)
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := eng.ExecuteInExecution(ctx, execution, "42", base)
		done <- execErr
	}()

	select {
	case <-fake.emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the agent to emit its scripted events")
	}

	// Batching defers persistence to ~250ms intervals; the guarantee that
	// guards a cancelled run's tail is the final Close on Execute's return
	// (issue 489), asserted below, not synchronous per-emit writes. Cancel
	// now so Execute returns and its cancel-immune Close runs.
	cancel()
	if execErr := <-done; execErr == nil {
		t.Fatalf("ExecuteInExecution err = nil, want non-nil (the run was cancelled mid-stream)")
	}

	final, err := engine.LoadTranscript(context.Background(), store, execution.ID, "42")
	if err != nil {
		t.Fatalf("LoadTranscript (post-cancellation): %v", err)
	}
	if len(final) != 2 {
		t.Fatalf("got %d transcript events after cancellation, want 2 — the last recorded event must be the one immediately before the wedge, not lost", len(final))
	}
	if final[0].Text != "starting work" {
		t.Fatalf("final[0] = %+v, want the first emitted message", final[0])
	}
	if final[1].ToolName != "Bash" || final[1].ToolCallID != "tool-1" {
		t.Fatalf("final[1] = %+v, want the emitted tool call", final[1])
	}
}

// transcriptEmittingReviewer is an engine.Engine.Reviewer double that emits
// one scripted TranscriptEvent per subagent via req.TranscriptSinkFor (when
// set) before returning a programmed review.Result — standing in for
// internal/review/agentreviewer's per-axis fan-out, without depending on
// that package from the engine's tests.
type transcriptEmittingReviewer struct {
	subagents []string
	result    review.Result
}

func (r *transcriptEmittingReviewer) Review(_ context.Context, req review.Request) (review.Result, error) {
	if req.TranscriptSinkFor != nil {
		for _, subagent := range r.subagents {
			sink := req.TranscriptSinkFor(subagent)
			sink.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "reviewing " + subagent})
		}
	}
	return r.result, nil
}

var _ review.Reviewer = (*transcriptEmittingReviewer)(nil)

// TestExecute_PersistsReviewAgentTranscriptEvents is issue #219's check:
// the review agent's transcripts must be persisted to transcript_events as
// they occur, exactly as the execution agent's are, with each subagent's
// (review axis's) events tagged with the REVIEWING phase and its own
// subagent name so the two never collide in one table.
func TestExecute_PersistsReviewAgentTranscriptEvents(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{"42": {ID: "42"}})
	te.fake.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	reviewer := &transcriptEmittingReviewer{
		subagents: []string{"bugs", "quality", "docs"},
		result:    review.Result{Verdict: review.VerdictApproved, Summary: "looks good"},
	}
	te.eng.Reviewer = reviewer
	te.eng.Diff = &stubDiff{diff: "diff --git a/foo b/foo"}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	transcript, err := engine.LoadTranscript(ctx, te.store, result.ExecutionID, "42")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}

	bySubagent := map[string]string{}
	for _, event := range transcript {
		if event.Phase == "REVIEWING" {
			bySubagent[event.Subagent] = event.Text
		}
	}
	for _, subagent := range reviewer.subagents {
		text, ok := bySubagent[subagent]
		if !ok {
			t.Fatalf("no REVIEWING transcript event for subagent %q; got %+v", subagent, transcript)
		}
		if text != "reviewing "+subagent {
			t.Fatalf("subagent %q event text = %q, want %q", subagent, text, "reviewing "+subagent)
		}
	}
	if len(bySubagent) != len(reviewer.subagents) {
		t.Fatalf("got %d distinct REVIEWING subagents, want %d: %+v", len(bySubagent), len(reviewer.subagents), transcript)
	}
}
