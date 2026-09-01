package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/storage"
)

// openAgentInvocationTestStore opens a fresh, migrated SQLiteStore backed
// by a temp file — this file's tests need a real Store so the agent_runs
// foreign keys (execution/issue rows) are honored, unlike the lighter
// hand-rolled storage.Store doubles other *_internal_test.go files use.
func openAgentInvocationTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// poisonAgent fails the test if Execute is ever called on it — it stands
// in for Engine.Agent in this file's tests, which prove the Agent that
// actually runs is the one env.Agent() returns, not this field, when both
// are given a chance to differ (constructorfleet/forge#302).
type poisonAgent struct {
	t *testing.T
}

func (p poisonAgent) Execute(context.Context, agent.AgentRequest) (agent.AgentResult, error) {
	p.t.Fatal("Engine.Agent.Execute was called directly; want the Agent invoked through env.Agent()")
	return agent.AgentResult{}, errors.New("unreachable")
}

var _ agent.Agent = poisonAgent{}

// TestExecuteAgent_InvokesAgentThroughEnvironment proves executeAgent runs
// the coding Agent via env.Agent() in the Workspace (constructorfleet/
// forge#302's "Agent invoked via env.Agent()" acceptance criterion) rather
// than Engine's own Agent field: env.Agent() here is a FakeAgent distinct
// from Engine.Agent, which is set to poisonAgent so the test fails loudly
// if executeAgent ever falls back to it.
func TestExecuteAgent_InvokesAgentThroughEnvironment(t *testing.T) {
	store := openAgentInvocationTestStore(t)
	ctx := context.Background()

	eng := &Engine{
		Store:          store,
		Agent:          poisonAgent{t: t},
		Now:            time.Now,
		NewExecutionID: func() string { return "exec-1" },
	}

	execRow, err := eng.StartExecution(ctx, "base-sha")
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	issue := domain.Issue{ID: "42", ExecutionID: execRow.ID, State: domain.StatePreparing}
	if err := store.CreateIssue(ctx, issue); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	envAgent := agent.NewFakeAgent()
	envAgent.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})
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

	invocations := envAgent.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("got %d invocations on env.Agent(), want 1", len(invocations))
	}
	if invocations[0].WorkspacePath != env.Workspace().Path {
		t.Errorf("WorkspacePath = %q, want %q", invocations[0].WorkspacePath, env.Workspace().Path)
	}

	runs, err := store.AgentRunsByIssue(ctx, execRow.ID, "42")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d agent runs, want 1", len(runs))
	}
	if runs[0].Result != string(agent.StatusImplemented) {
		t.Errorf("AgentRun.Result = %q, want %q", runs[0].Result, agent.StatusImplemented)
	}
}
