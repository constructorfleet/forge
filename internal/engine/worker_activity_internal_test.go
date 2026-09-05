package engine

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// activityEmittingAgent emits one transcript event before returning a
// programmed result — standing in for a real Adapter that streams output as
// it works, the signal touchWorkerActivity relies on.
type activityEmittingAgent struct {
	result agent.AgentResult
}

func (a *activityEmittingAgent) Execute(_ context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	if req.Transcript != nil {
		req.Transcript.Emit(agent.TranscriptEvent{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "working"})
	}
	return a.result, nil
}

var _ agent.Agent = (*activityEmittingAgent)(nil)

// TestExecuteAgent_TouchesWorkerActivityOnTranscriptEmit proves the #463
// wiring: a transcript event streamed by the running Agent — the real
// per-invocation progress signal, not just the claim goroutine being alive
// — advances the Issue's tracked WorkerActivity, which is what lets
// RunWorkerHeartbeat tell "still working" from "wedged."
func TestExecuteAgent_TouchesWorkerActivityOnTranscriptEmit(t *testing.T) {
	store := openAgentInvocationTestStore(t)
	ctx := context.Background()

	var ticks int64
	clock := func() time.Time {
		n := atomic.AddInt64(&ticks, 1)
		return time.Unix(n, 0).UTC()
	}

	eng := &Engine{
		Store:          store,
		Agent:          poisonAgent{t: t},
		Now:            clock,
		NewExecutionID: func() string { return "exec-activity" },
	}

	execRow, err := eng.StartExecution(ctx, "base-sha")
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	issue := domain.Issue{ID: "42", ExecutionID: execRow.ID, State: domain.StatePreparing}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	activity := eng.startWorkerActivity(execRow.ID, "42")
	defer eng.stopWorkerActivity(execRow.ID, "42")
	initialTouch := activity.LastTouch()

	envAgent := &activityEmittingAgent{result: agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"}}
	env := execution.NewFakeEnvironmentWithAgent(
		domain.Workspace{IssueID: "42", Path: filepath.Join(t.TempDir(), "ws")},
		envAgent,
	)

	repoCtx := agent.RepositoryContext{BaseRevision: "base-sha"}
	if _, implemented, err := eng.continueAgent(ctx, execRow.ID, "42", env, repoCtx, issue, nil); err != nil {
		t.Fatalf("continueAgent: %v", err)
	} else if !implemented {
		t.Fatal("continueAgent: implemented = false, want true")
	}

	if !activity.LastTouch().After(initialTouch) {
		t.Fatalf("WorkerActivity.LastTouch() = %v, want later than %v after the Agent streamed a transcript event", activity.LastTouch(), initialTouch)
	}
}
