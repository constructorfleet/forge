package httpworker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/remote"
)

func newTestClient(t *testing.T, ag agent.Agent) (*Client, string, string) {
	t.Helper()
	srv, originPath, base := newTestServer(t, ag)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return NewClient(ts.URL, ts.Client()), originPath, base
}

func TestClient_PingSucceedsAgainstARunningServer(t *testing.T) {
	client, _, _ := newTestClient(t, agent.NewFakeAgent())
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClient_PingFailsAgainstAnUnreachableAddress(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", nil)
	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping: want an error against an unreachable address, got nil")
	}
}

func TestClient_ImplementsWorkerClient(t *testing.T) {
	var _ remote.WorkerClient = (*Client)(nil)
}

func TestClient_PrepareExecuteHeartbeatFetchResultCleanupRoundTrip(t *testing.T) {
	client, _, base := newTestClient(t, agent.NewFakeAgent())
	ctx := context.Background()

	handle, ws, err := client.PrepareWorkspace(ctx, execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-1", Base: base,
	})
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if ws.Path == "" {
		t.Fatal("PrepareWorkspace returned a Workspace with an empty Path")
	}

	if err := client.Heartbeat(ctx, handle); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	result, err := client.Execute(ctx, handle, execution.Command{Name: "touch", Command: "echo hi > out.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Execute ExitCode = %d, want 0 (stderr: %s)", result.ExitCode, result.Stderr)
	}

	commitResult, err := client.Execute(ctx, handle, execution.Command{
		Name:    "commit",
		Command: "git add out.txt && git -c user.email=w@example.com -c user.name=Worker commit -q -m worker-change",
	})
	if err != nil {
		t.Fatalf("Execute(commit): %v", err)
	}
	if commitResult.ExitCode != 0 {
		t.Fatalf("commit ExitCode = %d, want 0 (stderr: %s)", commitResult.ExitCode, commitResult.Stderr)
	}

	wr, err := client.FetchResult(ctx, handle)
	if err != nil {
		t.Fatalf("FetchResult: %v", err)
	}
	if len(wr.Bundle) == 0 {
		t.Error("FetchResult returned an empty bundle")
	}
	if wr.HeadSHA == "" {
		t.Error("FetchResult returned an empty HeadSHA")
	}

	if err := client.Cleanup(ctx, handle); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := client.Execute(ctx, handle, execution.Command{Name: "noop", Command: "true"}); err == nil {
		t.Error("Execute after Cleanup: want an error for the now-unknown handle, got nil")
	}
}

func TestClient_RunAgentReturnsAgentResult(t *testing.T) {
	ag := agent.NewFakeAgent()
	ag.ProgramResult("issue-1", agent.AgentResult{Status: agent.StatusImplemented, Summary: "done"})
	client, _, base := newTestClient(t, ag)
	ctx := context.Background()

	handle, _, err := client.PrepareWorkspace(ctx, execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-1", Base: base,
	})
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}

	result, err := client.RunAgent(ctx, handle, agent.AgentRequest{Issue: domain.Issue{ID: "issue-1"}})
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if result.Status != agent.StatusImplemented || result.Summary != "done" {
		t.Errorf("RunAgent result = %+v, want {StatusImplemented done ...}", result)
	}
}

func TestClient_ExecuteAgainstUnknownHandleReturnsError(t *testing.T) {
	client, _, _ := newTestClient(t, agent.NewFakeAgent())
	if _, err := client.Execute(context.Background(), "no-such-handle", execution.Command{Name: "x", Command: "true"}); err == nil {
		t.Fatal("Execute: want an error for an unknown handle, got nil")
	}
}

func TestClient_TransportErrorWhenServerUnreachable(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", &http.Client{Timeout: 2 * time.Second})
	_, _, err := client.PrepareWorkspace(context.Background(), execution.WorkspaceRequest{ExecutionID: "e", IssueID: "i", Base: "deadbeef"})
	if err == nil {
		t.Fatal("PrepareWorkspace: want a transport error, got nil")
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("unexpected context.Canceled: %v", err)
	}
}
