package remote

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gittest"
)

// workerBundle simulates a worker: it clones root at base, commits one
// change on top of it, and returns a Git bundle spanning base..HEAD plus
// that HEAD's SHA — exactly what a real worker's FetchResult would hand
// back over the wire, with no shared filesystem with root.
func workerBundle(t *testing.T, root, base string) (bundle []byte, headSHA string) {
	t.Helper()
	workerRepo := t.TempDir()
	gittest.RunGit(t, workerRepo, "clone", "-q", root, ".")
	gittest.RunGit(t, workerRepo, "config", "user.email", "worker@example.com")
	gittest.RunGit(t, workerRepo, "config", "user.name", "Worker")
	gittest.RunGit(t, workerRepo, "checkout", "-q", base)

	if err := os.WriteFile(workerRepo+"/worker.txt", []byte("worker change\n"), 0o644); err != nil {
		t.Fatalf("write worker.txt: %v", err)
	}
	gittest.RunGit(t, workerRepo, "add", "worker.txt")
	gittest.RunGit(t, workerRepo, "commit", "-q", "-m", "worker change")
	headSHA = strings.TrimSpace(gittest.RunGit(t, workerRepo, "rev-parse", "HEAD"))

	bundlePath := workerRepo + "/out.bundle"
	gittest.RunGit(t, workerRepo, "bundle", "create", bundlePath, base+"..HEAD")
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	return data, headSHA
}

func newTestRemoteEnv(t *testing.T, ws domain.Workspace, base string) (execution.ExecutionEnvironment, *FakeWorker) {
	t.Helper()
	backend, worker := newTestBackend(ws)
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-42", Base: base,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return env, worker
}

func TestBundlePublisher_CommitImportsWorkerBundleIntoCanonicalRepo(t *testing.T) {
	root, _, base := gittest.NewTempRepoWithOrigin(t)
	bundle, headSHA := workerBundle(t, root, base)

	branch := "forge/exec1/issue-42"
	ws := domain.Workspace{IssueID: "issue-42", Path: "/remote/issue-42", Branch: branch}
	env, worker := newTestRemoteEnv(t, ws, base)
	worker.ProgramFetchResult(WorkerResult{Bundle: bundle, HeadSHA: headSHA})

	publisher := NewBundlePublisher(root)
	sha, err := publisher.Commit(context.Background(), env, "unused message")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if sha != headSHA {
		t.Errorf("Commit() sha = %q, want %q", sha, headSHA)
	}

	got := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "refs/heads/"+branch))
	if got != headSHA {
		t.Errorf("canonical repo's %s = %q, want %q", branch, got, headSHA)
	}
	if worker.FetchResultCalls() != 1 {
		t.Errorf("FetchResultCalls() = %d, want 1", worker.FetchResultCalls())
	}
}

func TestBundlePublisher_CommitPropagatesFetchResultError(t *testing.T) {
	root, _, base := gittest.NewTempRepoWithOrigin(t)
	ws := domain.Workspace{IssueID: "issue-42", Branch: "forge/exec1/issue-42"}
	env, worker := newTestRemoteEnv(t, ws, base)
	worker.ProgramFetchError(errWorkerUnreachable)

	publisher := NewBundlePublisher(root)
	if _, err := publisher.Commit(context.Background(), env, "message"); err == nil {
		t.Fatal("Commit() error = nil, want the worker's fetch error")
	}
}

func TestBundlePublisher_CommitRejectsEmptyBundle(t *testing.T) {
	root, _, base := gittest.NewTempRepoWithOrigin(t)
	ws := domain.Workspace{IssueID: "issue-42", Branch: "forge/exec1/issue-42"}
	env, worker := newTestRemoteEnv(t, ws, base)
	worker.ProgramFetchResult(WorkerResult{HeadSHA: "deadbeef"})

	publisher := NewBundlePublisher(root)
	if _, err := publisher.Commit(context.Background(), env, "message"); err == nil {
		t.Fatal("Commit() error = nil, want an error for an empty bundle")
	}
}

func TestBundlePublisher_PushPublishesImportedBranchToOrigin(t *testing.T) {
	root, originPath, base := gittest.NewTempRepoWithOrigin(t)
	bundle, headSHA := workerBundle(t, root, base)

	branch := "forge/exec1/issue-42"
	ws := domain.Workspace{IssueID: "issue-42", Branch: branch}
	env, worker := newTestRemoteEnv(t, ws, base)
	worker.ProgramFetchResult(WorkerResult{Bundle: bundle, HeadSHA: headSHA})

	publisher := NewBundlePublisher(root)
	if _, err := publisher.Commit(context.Background(), env, "message"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := publisher.Push(context.Background(), env, branch); err != nil {
		t.Fatalf("Push: %v", err)
	}

	got := strings.TrimSpace(gittest.RunGit(t, originPath, "rev-parse", "refs/heads/"+branch))
	if got != headSHA {
		t.Errorf("origin's %s = %q, want %q", branch, got, headSHA)
	}
	subject := strings.TrimSpace(gittest.RunGit(t, originPath, "log", "-1", "--pretty=%s", "refs/heads/"+branch))
	if subject != "worker change" {
		t.Errorf("origin's %s subject = %q, want %q", branch, subject, "worker change")
	}
}

// TestCredentialBoundary_PublishNeverCallsWorkerExecuteOrAgent proves, by
// observed behavior, that importing a bundle and pushing it never reaches
// the worker again: the credential-bearing operations (Commit's git import,
// Push's git push) run entirely controller-side, disjoint from the
// WorkerClient calls (Execute/RunAgent) that ran the Worker's own coding
// work (constructorfleet/forge#340's credential boundary).
func TestCredentialBoundary_PublishNeverCallsWorkerExecuteOrAgent(t *testing.T) {
	root, _, base := gittest.NewTempRepoWithOrigin(t)
	bundle, headSHA := workerBundle(t, root, base)

	branch := "forge/exec1/issue-42"
	ws := domain.Workspace{IssueID: "issue-42", Branch: branch}
	env, worker := newTestRemoteEnv(t, ws, base)
	worker.ProgramFetchResult(WorkerResult{Bundle: bundle, HeadSHA: headSHA})

	publisher := NewBundlePublisher(root)
	if _, err := publisher.Commit(context.Background(), env, "message"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := publisher.Push(context.Background(), env, branch); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if len(worker.Executed()) != 0 {
		t.Errorf("worker.Executed() = %+v, want none — publishing must not reach the worker", worker.Executed())
	}
	if worker.AgentCalls() != 0 {
		t.Errorf("worker.AgentCalls() = %d, want 0 — publishing must not reach the worker", worker.AgentCalls())
	}
}

// TestCredentialBoundary_WorkerNeverSeesMutationCredentials proves, by
// observed behavior, that a Worker's in-flight work (Quality Gates and the
// Agent) never carries an SCM or tracker credential to the worker, even
// when one is present in Forge's own process environment — mirroring
// LocalHost/Container (ADR 0021, constructorfleet/forge#335) for the
// Remote backend.
func TestCredentialBoundary_WorkerNeverSeesMutationCredentials(t *testing.T) {
	const credentialValue = "ghp_super-secret-tracker-token"
	t.Setenv("GITHUB_TOKEN", credentialValue)

	ws := domain.Workspace{IssueID: "issue-42"}
	backend, worker := newTestBackend(ws)
	worker.ProgramExecuteResult("gate", execution.Result{Name: "gate", ExitCode: 0})
	worker.ProgramAgentResult("", agent.AgentResult{Status: agent.StatusImplemented})
	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if _, err := env.Execute(context.Background(), execution.Command{Name: "gate", Command: "echo gate ran"}); err != nil {
		t.Fatalf("Execute(gate): %v", err)
	}
	if _, err := env.Agent().Execute(context.Background(), agent.AgentRequest{Issue: domain.Issue{Title: "do the work"}}); err != nil {
		t.Fatalf("Agent().Execute: %v", err)
	}

	for _, call := range worker.Executed() {
		if strings.Contains(call.Command, credentialValue) || strings.Contains(call.Stdin, credentialValue) {
			t.Errorf("Command %+v carries the tracker credential", call)
		}
		for _, arg := range call.Args {
			if strings.Contains(arg, credentialValue) {
				t.Errorf("Command.Args %v carries the tracker credential", call.Args)
			}
		}
		for _, e := range call.Env {
			if strings.Contains(e, credentialValue) || strings.HasPrefix(e, "GITHUB_TOKEN=") {
				t.Errorf("Command.Env %v carries the tracker credential", call.Env)
			}
		}
	}
}
