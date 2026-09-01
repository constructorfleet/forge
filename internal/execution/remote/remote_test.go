package remote

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

func newTestBackend(ws domain.Workspace) (*Backend, *FakeWorker) {
	worker := NewFakeWorker(ws)
	return NewBackend(worker), worker
}

func TestBackend_PreparePassesRequestToWorkerAndReturnsItsWorkspace(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42", Path: "/remote/issue-42", Branch: "forge/exec1/issue-42"}
	backend, worker := newTestBackend(ws)

	req := execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42", Base: "deadbeef"}
	env, err := backend.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if env.Workspace() != ws {
		t.Errorf("Workspace() = %+v, want %+v", env.Workspace(), ws)
	}

	prepared := worker.Prepared()
	if len(prepared) != 1 || prepared[0] != req {
		t.Errorf("worker.Prepared() = %+v, want [%+v]", prepared, req)
	}
}

func TestBackend_PrepareReportsWorkerFailure(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramPrepareError(errWorkerUnreachable)

	_, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err == nil {
		t.Fatal("Prepare() error = nil, want the worker's error")
	}
}

func TestEnvironment_ExecuteRunsCommandOnWorker(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramExecuteResult("build", execution.Result{Name: "build", ExitCode: 0, Stdout: "ok"})

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Execute(context.Background(), execution.Command{Name: "build", Command: "make build"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "ok" {
		t.Errorf("Execute() = %+v, want programmed result", result)
	}

	executed := worker.Executed()
	if len(executed) != 1 || executed[0].Name != "build" {
		t.Errorf("worker.Executed() = %+v, want one build command", executed)
	}
}

func TestEnvironment_AgentRunsOnWorker(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramAgentResult("issue-42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Agent().Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-42"}})
	if err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}
	if result.Status != agent.StatusImplemented || result.Summary != "done" {
		t.Errorf("Agent().Execute() = %+v, want programmed result", result)
	}
}

func TestEnvironment_CleanupTearsDownWorkerWorkspace(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if calls := worker.CleanedUp(); len(calls) != 1 {
		t.Errorf("worker.CleanedUp() = %+v, want one cleanup call", calls)
	}
}

var _ execution.ExecutionBackend = (*Backend)(nil)
