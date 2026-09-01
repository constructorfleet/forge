package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/execution/remote"
	"github.com/Teagan42/forge/internal/execution/remote/httpworker"
	"github.com/Teagan42/forge/internal/gittest"
)

// TestExecute_RemoteWorker_VanishedDuringAgent_OverRealTransport_RetriesWithinBudget
// pins issue #360 acceptance criterion 5 over the concrete httpworker
// transport, not remote.FakeWorker. The Engine drives a real worker daemon
// over real HTTP; the daemon answers Prepare normally, then the Agent call
// vanishes mid-execution. Once recovery confirms the ExecutionLease has
// lapsed, the Engine must leave the Issue in a non-terminal, retriable
// state — its retry budget is not spent down to a terminal FAILED — so a
// later run retries it. This is the real-transport twin of
// TestExecute_RemoteWorker_VanishedDuringAgent_RoutesToLostNotFailed.
func TestExecute_RemoteWorker_VanishedDuringAgent_OverRealTransport_RetriesWithinBudget(t *testing.T) {
	// Controller-side canonical repository plus a shared bare origin; the
	// worker gets its own read-only clone of that origin, never the
	// controller's checkout, matching the httpworker end-to-end test.
	root, originPath, base := gittest.NewTempRepoWithOrigin(t)
	workerRoot := t.TempDir()
	gittest.RunGit(t, workerRoot, "clone", "-q", originPath, ".")
	gittest.RunGit(t, workerRoot, "config", "user.email", "worker@example.com")
	gittest.RunGit(t, workerRoot, "config", "user.name", "Worker")

	srv, err := httpworker.NewServer(workerRoot, "origin", agent.NewFakeAgent())
	if err != nil {
		t.Fatalf("httpworker.NewServer: %v", err)
	}

	// "/v1/agent" is httpworker's Agent route (see protocol.go). Dropping the
	// connection there makes the real Client.RunAgent return a transport
	// error — the ambiguous "worker vanished" signal that recovery, not the
	// error itself, resolves into LOST. Every other route is answered
	// normally, so Prepare succeeds and the loss happens mid-execution.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/agent" {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, herr := hj.Hijack(); herr == nil {
					_ = conn.Close()
					return
				}
			}
			http.Error(w, "worker vanished", http.StatusBadGateway)
			return
		}
		srv.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := httpworker.NewClient(ts.URL, ts.Client())

	recoverCalls := 0
	recover := func(_ context.Context, executionID, issueID string) (bool, error) {
		recoverCalls++
		if executionID == "" || issueID != "9" {
			t.Errorf("recover called with executionID=%q issueID=%q, want a non-empty executionID and issue 9", executionID, issueID)
		}
		return true, nil // the ExecutionLease has lapsed: the worker is lost
	}

	store := openTestStore(t)
	trk := &stubTracker{issues: map[string]domain.Issue{"9": {ID: "9"}}}
	eng := engine.New(store, trk, nil, nil, config.Default(), root)
	eng.Backend = remote.NewBackend(client, recover)
	const executionID = "exec-remote-lost-realtransport"
	eng.NewExecutionID = func() string { return executionID }

	_, err = eng.Execute(context.Background(), "9", base)
	if err == nil {
		t.Fatal("Execute: want an error when the worker is lost over the real transport, got nil")
	}
	if recoverCalls != 1 {
		t.Fatalf("recovery was consulted %d times, want exactly 1", recoverCalls)
	}

	issue, getErr := store.GetIssue(context.Background(), executionID, "9")
	if getErr != nil {
		t.Fatalf("GetIssue: %v", getErr)
	}
	if issue.State == domain.StateFailed || issue.State.IsTerminal() {
		t.Fatalf("issue.State = %s, want a non-terminal state left retriable by loss recovery, not FAILED/terminal", issue.State)
	}
}
