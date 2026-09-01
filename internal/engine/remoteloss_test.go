package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/remote"
	"github.com/Teagan42/forge/internal/gittest"
)

// errWorkerVanished stands in for the transport error a real WorkerClient
// returns when it cannot reach the worker (e.g. the RPC times out) — the
// ambiguous signal that recovery, not the error itself, resolves into
// either an ordinary failure or LOST.
var errWorkerVanished = errors.New("remote: worker vanished")

// newRemoteTestEngine wires an Engine to the Remote backend against a
// FakeWorker, mirroring newTestEngine's local-backend setup: the same
// stubTracker and SQLite store, but every Command/Agent call runs through
// the FakeWorker instead of the gateCommandSwitch/FakeAgent fakeBackend
// uses. recover is passed straight to remote.NewBackend, so a test can
// program whether a WorkerClient error represents a lost worker. The
// FakeWorker's Workspace points at the same temp repository newTestEngine
// uses, so repocontext.Compile (which reads real files from the Workspace
// path) works exactly as it does for the local backend's tests.
func newRemoteTestEngine(t *testing.T, issueID string, issues map[string]domain.Issue, recover remote.RecoverFunc) (testEngine, *remote.FakeWorker) {
	t.Helper()
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: issues}
	worker := remote.NewFakeWorker(domain.Workspace{IssueID: issueID, Path: repoRoot})
	eng := engine.New(store, trk, nil, nil, config.Default(), repoRoot)
	eng.Backend = remote.NewBackend(worker, recover)
	return testEngine{eng: eng, store: store, base: base, trk: trk}, worker
}

// TestExecute_RemoteWorker_ReportedAgentFailure_RoutesThroughExistingFailurePath
// pins acceptance criterion 1: a worker-reported Agent failure
// (agent.StatusFailed, no error) drives the Issue to FAILED through the
// Engine's ordinary failure handling, exactly as TestExecute_Failed_
// RoutesDirectlyToFailed does for a local Agent — and never consults
// recovery, since a reported failure is never a candidate for LOST.
func TestExecute_RemoteWorker_ReportedAgentFailure_RoutesThroughExistingFailurePath(t *testing.T) {
	recoverCalls := 0
	recover := func(_ context.Context, _, _ string) (bool, error) {
		recoverCalls++
		return false, nil
	}

	te, worker := newRemoteTestEngine(t, "9", map[string]domain.Issue{"9": {ID: "9"}}, recover)
	worker.ProgramAgentResult("9", agent.AgentResult{Status: agent.StatusFailed, Summary: "could not proceed"})

	result, err := te.eng.Execute(context.Background(), "9", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if recoverCalls != 0 {
		t.Errorf("recovery was consulted %d times, want 0 for a reported failure", recoverCalls)
	}
}

// TestExecute_RemoteWorker_ReportedGateFailure_RoutesThroughExistingFailurePath
// pins the other half of acceptance criterion 1: a worker-reported Quality
// Gate failure (a failing execution.Result, no error) drives the Issue to
// FAILED through the Engine's ordinary gate-repair/retry-budget path,
// exactly as TestExecute_QualityGateFails_RoutesToFailedWithDiagnostic does
// for a local gate — and never consults recovery.
func TestExecute_RemoteWorker_ReportedGateFailure_RoutesThroughExistingFailurePath(t *testing.T) {
	recoverCalls := 0
	recover := func(_ context.Context, _, _ string) (bool, error) {
		recoverCalls++
		return false, nil
	}

	te, worker := newRemoteTestEngine(t, "21", map[string]domain.Issue{"21": {ID: "21"}}, recover)
	worker.ProgramAgentResult("21", agent.AgentResult{Status: agent.StatusImplemented})
	worker.ProgramExecuteResult("test", execution.Result{Name: "test", ExitCode: 1, Stderr: "assertion failed"})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry.Gate = 0

	result, err := te.eng.Execute(context.Background(), "21", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if recoverCalls != 0 {
		t.Errorf("recovery was consulted %d times, want 0 for a reported gate failure", recoverCalls)
	}
}

// TestExecute_RemoteWorker_VanishedDuringAgent_RoutesToLostNotFailed pins
// acceptance criteria 2 and 3: a WorkerClient transport error during the
// Agent call, once recovery confirms the ExecutionLease has lapsed, must
// not drive the Issue to FAILED. Instead the Issue stays in its pre-loss
// IssueState (IMPLEMENTING) so it can be retried, and Execute's returned
// error is the lost-worker error, not a masked ordinary failure.
func TestExecute_RemoteWorker_VanishedDuringAgent_RoutesToLostNotFailed(t *testing.T) {
	recoverCalls := 0
	recover := func(_ context.Context, executionID, issueID string) (bool, error) {
		recoverCalls++
		if executionID == "" || issueID != "9" {
			t.Errorf("recover called with executionID=%q issueID=%q, want a non-empty executionID and issue 9", executionID, issueID)
		}
		return true, nil
	}

	te, worker := newRemoteTestEngine(t, "9", map[string]domain.Issue{"9": {ID: "9"}}, recover)
	worker.ProgramAgentError("9", errWorkerVanished)
	const executionID = "exec-remote-lost"
	te.eng.NewExecutionID = func() string { return executionID }

	_, err := te.eng.Execute(context.Background(), "9", te.base)
	if err == nil {
		t.Fatal("Execute: want an error when the worker is lost, got nil")
	}
	if recoverCalls != 1 {
		t.Fatalf("recovery was consulted %d times, want exactly 1", recoverCalls)
	}

	issue, getErr := te.store.GetIssue(context.Background(), executionID, "9")
	if getErr != nil {
		t.Fatalf("GetIssue: %v", getErr)
	}
	if issue.State == domain.StateFailed || issue.State.IsTerminal() {
		t.Fatalf("issue.State = %s, want a non-terminal state left unchanged by loss recovery, not FAILED/terminal", issue.State)
	}
}
