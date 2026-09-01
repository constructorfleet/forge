package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// recoverSpy is a RecoverFunc test double: it records every call and
// returns the programmed (lost, err) pair, so a test can assert both the
// outcome environment/remoteAgent produce and whether recovery ran at all.
type recoverSpy struct {
	lost bool
	err  error

	calls []struct{ executionID, issueID string }
}

func (s *recoverSpy) Recover(_ context.Context, executionID, issueID string) (bool, error) {
	s.calls = append(s.calls, struct{ executionID, issueID string }{executionID, issueID})
	return s.lost, s.err
}

var errTransport = errors.New("remote: transport failure")

func TestEnvironment_Execute_ReportedFailure_NeverConsultsRecovery(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	worker.ProgramExecuteResult("test", execution.Result{Name: "test", ExitCode: 1, Stderr: "assertion failed"})
	spy := &recoverSpy{}
	backend := NewBackend(worker, spy.Recover)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Execute(context.Background(), execution.Command{Name: "test"})
	if err != nil {
		t.Fatalf("Execute: %v, want nil (a reported failure is a Result, not an error)", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
	if len(spy.calls) != 0 {
		t.Errorf("recovery was consulted %d times, want 0: a reported failure is never a candidate for LOST", len(spy.calls))
	}
}

func TestEnvironment_Execute_TransportError_NoRecoverConfigured_ReturnsRawError(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	worker.ProgramExecuteError("test", errTransport)
	backend := NewBackend(worker, nil)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = env.Execute(context.Background(), execution.Command{Name: "test"})
	if !errors.Is(err, errTransport) {
		t.Fatalf("Execute() error = %v, want it to wrap %v", err, errTransport)
	}
	if errors.Is(err, execution.ErrLost) {
		t.Error("Execute() error wraps execution.ErrLost with no RecoverFunc configured, want it not to")
	}
}

func TestEnvironment_Execute_TransportError_LeaseLapsed_RoutesToLost(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	worker.ProgramExecuteError("test", errTransport)
	spy := &recoverSpy{lost: true}
	backend := NewBackend(worker, spy.Recover)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = env.Execute(context.Background(), execution.Command{Name: "test"})
	if !errors.Is(err, execution.ErrLost) {
		t.Fatalf("Execute() error = %v, want it to wrap execution.ErrLost", err)
	}
	if len(spy.calls) != 1 || spy.calls[0].executionID != "exec1" || spy.calls[0].issueID != "issue-42" {
		t.Errorf("recovery calls = %+v, want one call for exec1/issue-42", spy.calls)
	}
}

func TestEnvironment_Execute_TransportError_LeaseHealthy_ReturnsRawErrorNotLost(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	worker.ProgramExecuteError("test", errTransport)
	spy := &recoverSpy{lost: false}
	backend := NewBackend(worker, spy.Recover)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = env.Execute(context.Background(), execution.Command{Name: "test"})
	if !errors.Is(err, errTransport) {
		t.Fatalf("Execute() error = %v, want it to still wrap %v", err, errTransport)
	}
	if errors.Is(err, execution.ErrLost) {
		t.Error("Execute() error wraps execution.ErrLost though the lease had not lapsed, want a plain failure")
	}
}

func TestRemoteAgent_Execute_ReportedFailure_NeverConsultsRecovery(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	worker.ProgramAgentResult("issue-42", agent.AgentResult{Status: agent.StatusFailed, Summary: "could not proceed"})
	spy := &recoverSpy{}
	backend := NewBackend(worker, spy.Recover)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := env.Agent().Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-42"}})
	if err != nil {
		t.Fatalf("Agent().Execute: %v, want nil (a reported failure is a Status, not an error)", err)
	}
	if result.Status != agent.StatusFailed {
		t.Errorf("Status = %s, want FAILED", result.Status)
	}
	if len(spy.calls) != 0 {
		t.Errorf("recovery was consulted %d times, want 0", len(spy.calls))
	}
}

func TestRemoteAgent_Execute_TransportError_LeaseLapsed_RoutesToLost(t *testing.T) {
	ws := domain.Workspace{IssueID: "issue-42"}
	worker := NewFakeWorker(ws)
	worker.ProgramAgentError("issue-42", errTransport)
	spy := &recoverSpy{lost: true}
	backend := NewBackend(worker, spy.Recover)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	_, err = env.Agent().Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{ID: "issue-42"}})
	if !errors.Is(err, execution.ErrLost) {
		t.Fatalf("Agent().Execute() error = %v, want it to wrap execution.ErrLost", err)
	}
	if len(spy.calls) != 1 {
		t.Errorf("recovery calls = %d, want 1", len(spy.calls))
	}
}
