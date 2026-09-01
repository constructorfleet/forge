package httpworker

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/remote"
	"github.com/Teagan42/forge/internal/gittest"
)

// TestRemoteBackend_EndToEndOverRealTransport is the one live-transport
// proof issue #345 asks for: every behavior the earlier Remote-backend
// tickets validated against remote.FakeWorker (prepare-at-commit, run a
// Command and the Agent remotely, return a bundle, publish through the
// controller, and recover from a lost worker) holds when the Remote
// backend drives a real worker daemon over real HTTP, not an in-memory
// fake. The Remote backend, WorkerClient seam, and Engine are exercised
// completely unchanged — only the WorkerClient implementation underneath
// them (httpworker.Client/Server) is new.
func TestRemoteBackend_EndToEndOverRealTransport(t *testing.T) {
	// The canonical repository lives controller-side (root), with a bare
	// "origin" it eventually publishes to — exactly BundlePublisher's
	// existing unit tests' setup (bundle_test.go).
	root, originPath, base := gittest.NewTempRepoWithOrigin(t)

	// The worker's own local clone: its read-only fetch source is the same
	// bare "origin", never root or a shared filesystem with the
	// controller.
	workerRoot := t.TempDir()
	gittest.RunGit(t, workerRoot, "clone", "-q", originPath, ".")
	gittest.RunGit(t, workerRoot, "config", "user.email", "worker@example.com")
	gittest.RunGit(t, workerRoot, "config", "user.name", "Worker")

	fakeAgent := agent.NewFakeAgent()
	fakeAgent.ProgramResult("issue-42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "implemented the change"})

	srv, err := NewServer(workerRoot, "origin", fakeAgent)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv)

	client := NewClient(ts.URL, ts.Client())
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: want the daemon reachable before dispatching work, got %v", err)
	}

	var lost bool
	var recoverCalls int
	recover := func(_ context.Context, _, _ string) (bool, error) {
		recoverCalls++
		return lost, nil
	}
	backend := remote.NewBackend(client, recover)

	// Baseline of origin's refs before any worker work. The worker holds a
	// read-only clone of origin and must never push; the snapshot below must
	// stay unchanged until the controller publishes.
	originRefsBefore := gittest.RunGit(t, originPath, "for-each-ref", "--format=%(objectname) %(refname)")

	ctx := context.Background()
	env, err := backend.Prepare(ctx, execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Run a Command remotely: the worker daemon executes it as a real
	// subprocess in its own Workspace, not the controller's.
	writeResult, err := env.Execute(ctx, execution.Command{
		Name:    "write-change",
		Command: "echo 'worker change' > worker.txt && git add worker.txt",
	})
	if err != nil {
		t.Fatalf("Execute(write-change): %v", err)
	}
	if writeResult.ExitCode != 0 {
		t.Fatalf("write-change ExitCode = %d, want 0 (stderr: %s)", writeResult.ExitCode, writeResult.Stderr)
	}

	commitResult, err := env.Execute(ctx, execution.Command{
		Name:    "commit",
		Command: "git -c user.email=worker@example.com -c user.name=Worker commit -q -m 'worker change'",
	})
	if err != nil {
		t.Fatalf("Execute(commit): %v", err)
	}
	if commitResult.ExitCode != 0 {
		t.Fatalf("commit ExitCode = %d, want 0 (stderr: %s)", commitResult.ExitCode, commitResult.Stderr)
	}

	// Run the Agent remotely.
	agentResult, err := env.Agent().Execute(ctx, agent.AgentRequest{
		Issue: domain.Issue{ID: "issue-42", Title: "do the work"},
	})
	if err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}
	if agentResult.Status != agent.StatusImplemented {
		t.Fatalf("Agent().Execute status = %s, want IMPLEMENTED", agentResult.Status)
	}

	// Publish through the controller: import the worker's bundle into the
	// canonical repository and push it, matching bundle_test.go's
	// assertions for the fake-backed path exactly.
	publisher := remote.NewBundlePublisher(root)
	sha, err := publisher.Commit(ctx, env, "unused message")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	branch := env.Workspace().Branch
	gotRoot := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "refs/heads/"+branch))
	if gotRoot != sha {
		t.Errorf("canonical repo's %s = %q, want %q", branch, gotRoot, sha)
	}

	// Read-only / never-pushed proof (issue #360, acceptance criterion 3).
	// The worker's change reached the canonical repository through the
	// controller's Commit above, but it must not have reached origin: only
	// the controller publishes. origin's refs are unchanged since the
	// baseline and do not carry the worker's commit yet.
	originRefsBeforePush := gittest.RunGit(t, originPath, "for-each-ref", "--format=%(objectname) %(refname)")
	if originRefsBeforePush != originRefsBefore {
		t.Errorf("origin changed before the controller published; the worker must never push.\n before: %q\n after:  %q", originRefsBefore, originRefsBeforePush)
	}
	if strings.Contains(originRefsBeforePush, sha) {
		t.Errorf("origin carries the worker commit %s before the controller pushed; the worker must never push", sha)
	}
	// The worker built on exactly the controller-pinned base it fetched
	// read-only: base is an ancestor of the worker's commit, so merge-base
	// of the two is base itself.
	if mb := strings.TrimSpace(gittest.RunGit(t, root, "merge-base", base, sha)); mb != base {
		t.Errorf("worker commit %s is not built on the pinned base %s (merge-base=%s); Prepare must fetch the correct commit read-only", sha, base, mb)
	}

	if err := publisher.Push(ctx, env, branch); err != nil {
		t.Fatalf("Push: %v", err)
	}
	gotOrigin := strings.TrimSpace(gittest.RunGit(t, originPath, "rev-parse", "refs/heads/"+branch))
	if gotOrigin != sha {
		t.Errorf("origin's %s = %q, want %q", branch, gotOrigin, sha)
	}
	subject := strings.TrimSpace(gittest.RunGit(t, originPath, "log", "-1", "--pretty=%s", "refs/heads/"+branch))
	if subject != "worker change" {
		t.Errorf("origin's %s subject = %q, want %q", branch, subject, "worker change")
	}

	if err := env.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Recover from a lost worker: once the daemon stops answering, a
	// WorkerClient transport error must still be classified through
	// RecoverFunc exactly as it is against a fake worker's programmed
	// error (ADR 0024) — the real transport changes nothing about that
	// contract.
	lostEnv, err := backend.Prepare(ctx, execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-99", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare (before loss): %v", err)
	}
	ts.Close()
	lost = true

	_, err = lostEnv.Execute(ctx, execution.Command{Name: "noop", Command: "true"})
	if err == nil {
		t.Fatal("Execute after the worker vanished: want an error, got nil")
	}
	if !errors.Is(err, execution.ErrLost) {
		t.Errorf("Execute after the worker vanished: error = %v, want it to wrap execution.ErrLost", err)
	}
	if recoverCalls == 0 {
		t.Error("recover was never consulted for the transport error")
	}
}
