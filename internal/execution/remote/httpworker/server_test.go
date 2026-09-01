package httpworker

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gittest"
)

// newTestServer builds a Server whose primary checkout is a fresh clone of
// a temp "origin" repository, exactly as a real worker daemon would be
// configured: pointed at the repository it has read-only fetch access to.
// Returns the Server, the origin repository's path, and the seed commit
// SHA both root and origin share.
func newTestServer(t *testing.T, ag agent.Agent) (srv *Server, originPath, base string) {
	t.Helper()
	_, originPath, base = gittest.NewTempRepoWithOrigin(t)
	workerRoot := t.TempDir()
	gittest.RunGit(t, workerRoot, "clone", "-q", originPath, ".")
	gittest.RunGit(t, workerRoot, "config", "user.email", "worker@example.com")
	gittest.RunGit(t, workerRoot, "config", "user.name", "Worker")

	srv, err := NewServer(workerRoot, "origin", ag)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, originPath, base
}

func TestServer_HealthReportsOK(t *testing.T) {
	srv, _, _ := newTestServer(t, agent.NewFakeAgent())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + pathHealth)
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_PrepareFetchesBaseAndCreatesWorktree(t *testing.T) {
	srv, originPath, base := newTestServer(t, agent.NewFakeAgent())

	// Advance origin past what the worker's clone knows about, exactly as
	// a sibling change merging on the shared repository would, so Prepare
	// must fetch to find base.
	seed := t.TempDir()
	gittest.RunGit(t, seed, "clone", "-q", originPath, ".")
	if err := os.WriteFile(seed+"/extra.txt", []byte("more\n"), 0o644); err != nil {
		t.Fatalf("write extra.txt: %v", err)
	}
	gittest.RunGit(t, seed, "config", "user.email", "seed@example.com")
	gittest.RunGit(t, seed, "config", "user.name", "Seed")
	gittest.RunGit(t, seed, "add", "extra.txt")
	gittest.RunGit(t, seed, "commit", "-q", "-m", "extra")
	gittest.RunGit(t, seed, "push", "-q", "origin", "HEAD:main")
	newBase := strings.TrimSpace(gittest.RunGit(t, seed, "rev-parse", "HEAD"))
	_ = base

	handle, ws, err := srv.PrepareWorkspace(t.Context(), execution.WorkspaceRequest{
		ExecutionID: "exec1", IssueID: "issue-1", Base: newBase,
	})
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if handle == "" {
		t.Fatal("PrepareWorkspace returned an empty handle")
	}
	if ws.Path == "" {
		t.Fatal("PrepareWorkspace returned a Workspace with an empty Path")
	}
	if _, err := os.Stat(ws.Path + "/extra.txt"); err != nil {
		t.Errorf("worktree at %s does not contain the fetched commit's file: %v", ws.Path, err)
	}
}
