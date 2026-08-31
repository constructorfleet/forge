package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

func TestFakeBackend_PrepareReturnsProgrammedEnvironment(t *testing.T) {
	backend := NewFakeBackend()
	ws := domain.Workspace{IssueID: "issue-1", Path: "/tmp/ws", Branch: "forge/exec1/issue-1"}
	env := NewFakeEnvironment(ws)
	backend.ProgramResult("issue-1", env)

	got, err := backend.Prepare(context.Background(), WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-1", Base: "main"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != env {
		t.Errorf("Prepare returned a different environment than programmed")
	}
	if got.Workspace() != ws {
		t.Errorf("Workspace() = %+v, want %+v", got.Workspace(), ws)
	}
}

func TestFakeBackend_PrepareReturnsProgrammedError(t *testing.T) {
	backend := NewFakeBackend()
	wantErr := errors.New("boom")
	backend.ProgramError("issue-1", wantErr)

	_, err := backend.Prepare(context.Background(), WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-1"})
	if !errors.Is(err, wantErr) {
		t.Errorf("Prepare error = %v, want %v", err, wantErr)
	}
}

func TestFakeBackend_RecordsInvocations(t *testing.T) {
	backend := NewFakeBackend()
	backend.ProgramDefault(NewFakeEnvironment(domain.Workspace{}))

	req := WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-1", Base: "main"}
	if _, err := backend.Prepare(context.Background(), req); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got := backend.Invocations()
	if len(got) != 1 || got[0] != req {
		t.Errorf("Invocations() = %+v, want [%+v]", got, req)
	}
}

func TestFakeEnvironment_ExecuteReturnsProgrammedResult(t *testing.T) {
	env := NewFakeEnvironment(domain.Workspace{IssueID: "issue-1"})
	want := Result{Name: "test", ExitCode: 0, Stdout: "ok"}
	env.ProgramExecuteResult("test", want)

	got, err := env.Execute(context.Background(), Command{Name: "test", Command: "go test ./..."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != want {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
}

func TestFakeEnvironment_AgentReturnsProgrammedAgent(t *testing.T) {
	fakeAgent := agent.NewFakeAgent()
	env := NewFakeEnvironmentWithAgent(domain.Workspace{}, fakeAgent)

	if env.Agent() != fakeAgent {
		t.Errorf("Agent() did not return the programmed agent")
	}
}

func TestFakeEnvironment_CleanupRecordsCall(t *testing.T) {
	env := NewFakeEnvironment(domain.Workspace{})
	if env.CleanupCalled() {
		t.Fatalf("CleanupCalled() = true before Cleanup")
	}
	if err := env.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !env.CleanupCalled() {
		t.Errorf("CleanupCalled() = false after Cleanup")
	}
}

var (
	_ ExecutionBackend     = (*FakeBackend)(nil)
	_ ExecutionEnvironment = (*FakeEnvironment)(nil)
)
