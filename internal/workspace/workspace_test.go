package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit runs a git command against dir and fails the test on error. It is
// the test-side helper for driving the temporary Git repositories tests run
// against; the production code under test uses its own CommandRunner.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// newTempRepo creates a temporary Git repository with one commit on its
// default branch and returns its root path and the commit SHA.
func newTempRepo(t *testing.T) (root, initialSHA string) {
	t.Helper()
	root = t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-q", "-m", "initial")
	sha := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	return root, sha
}

func TestCreate_WorktreeAtExpectedPathAndBranch(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := NewManager(root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantPath := filepath.Join(root, ".forge", "worktrees", "exec1", "issue-42")
	if ws.Path != wantPath {
		t.Errorf("Path = %q, want %q", ws.Path, wantPath)
	}
	wantBranch := "forge/exec1/issue-42"
	if ws.Branch != wantBranch {
		t.Errorf("Branch = %q, want %q", ws.Branch, wantBranch)
	}
	if ws.IssueID != "issue-42" {
		t.Errorf("IssueID = %q, want issue-42", ws.IssueID)
	}
	if info, err := os.Stat(ws.Path); err != nil || !info.IsDir() {
		t.Errorf("worktree dir not created at %s: %v", ws.Path, err)
	}
}

func TestCreate_HonorsPerWorkerBase(t *testing.T) {
	root, base := newTempRepo(t)

	// Advance the primary checkout past base, simulating a dependency's
	// merged code landing after this Worker's base was captured.
	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	runGit(t, root, "add", "second.txt")
	runGit(t, root, "commit", "-q", "-m", "second")
	newerSHA := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	mgr := NewManager(root)

	// Worker A captured base at the older commit.
	wsA, err := mgr.Create(context.Background(), "exec1", "issue-a", base)
	if err != nil {
		t.Fatalf("Create issue-a: %v", err)
	}
	headA := strings.TrimSpace(runGit(t, wsA.Path, "rev-parse", "HEAD"))
	if headA != base {
		t.Errorf("issue-a HEAD = %s, want base %s", headA, base)
	}

	// Worker B (dependency-blocked) captured base at the newer commit.
	wsB, err := mgr.Create(context.Background(), "exec1", "issue-b", newerSHA)
	if err != nil {
		t.Fatalf("Create issue-b: %v", err)
	}
	headB := strings.TrimSpace(runGit(t, wsB.Path, "rev-parse", "HEAD"))
	if headB != newerSHA {
		t.Errorf("issue-b HEAD = %s, want newer %s", headB, newerSHA)
	}
}

func TestCreate_IdempotentReCreation(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := NewManager(root)

	ws1, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	ws2, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("second Create (idempotent) should not error: %v", err)
	}

	if ws1.Path != ws2.Path || ws1.Branch != ws2.Branch {
		t.Errorf("re-creation produced different Workspace: %+v vs %+v", ws1, ws2)
	}
}

func TestCreate_PrimaryCheckoutUntouched(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := NewManager(root)

	// Track every tracked file's content before Create; the worktree root
	// itself (an untracked directory holding the new worktrees) is
	// expected to appear, but no tracked file may change and HEAD/branch
	// must not move.
	trackedBefore := runGit(t, root, "diff", "HEAD", "--stat")
	branchBefore := strings.TrimSpace(runGit(t, root, "branch", "--show-current"))
	headBefore := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	if _, err := mgr.Create(context.Background(), "exec1", "issue-42", base); err != nil {
		t.Fatalf("Create: %v", err)
	}

	trackedAfter := runGit(t, root, "diff", "HEAD", "--stat")
	if trackedBefore != trackedAfter {
		t.Errorf("tracked files in primary checkout changed:\nbefore: %q\nafter:  %q", trackedBefore, trackedAfter)
	}

	branchAfter := strings.TrimSpace(runGit(t, root, "branch", "--show-current"))
	if branchBefore != branchAfter {
		t.Errorf("primary checkout branch moved from %q to %q", branchBefore, branchAfter)
	}

	headAfter := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	if headAfter != headBefore || headAfter != base {
		t.Errorf("primary checkout HEAD moved to %s, want %s", headAfter, base)
	}
}

func TestCleanup_RemovesDirAndWorktreeEntry(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := NewManager(root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Cleanup(context.Background(), "exec1", "issue-42"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after Cleanup: %v", err)
	}

	list := runGit(t, root, "worktree", "list", "--porcelain")
	if strings.Contains(list, ws.Path) {
		t.Errorf("git worktree list still references cleaned-up path:\n%s", list)
	}
}

func TestCleanup_MissingWorktreeIsNotAnError(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := NewManager(root)

	if err := mgr.Cleanup(context.Background(), "exec1", "never-created"); err != nil {
		t.Errorf("Cleanup of nonexistent workspace should be a no-op, got: %v", err)
	}
}

func TestValidate_ExistingWorktree(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := NewManager(root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok, err := mgr.Validate(context.Background(), "exec1", "issue-42")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !ok {
		t.Fatal("Validate: ok = false, want true for existing workspace")
	}
	if got.Path != ws.Path || got.Branch != ws.Branch {
		t.Errorf("Validate Workspace = %+v, want %+v", got, ws)
	}
}

func TestValidate_MissingWorktree(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := NewManager(root)

	_, ok, err := mgr.Validate(context.Background(), "exec1", "never-created")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if ok {
		t.Fatal("Validate: ok = true, want false for nonexistent workspace")
	}
}

func TestValidate_DirRemovedButWorktreeRegistered(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := NewManager(root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := os.RemoveAll(ws.Path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	_, ok, err := mgr.Validate(context.Background(), "exec1", "issue-42")
	if err == nil && ok {
		t.Fatal("Validate should not report a healthy worktree when its directory is missing")
	}
}

func TestCreate_ActionableErrorOnBadBase(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := NewManager(root)

	_, err := mgr.Create(context.Background(), "exec1", "issue-42", "not-a-real-ref")
	if err == nil {
		t.Fatal("Create with bad base ref should error")
	}
	if !strings.Contains(err.Error(), "not-a-real-ref") {
		t.Errorf("error %q should mention the offending ref", err.Error())
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("error %q should mention git for actionability", err.Error())
	}
}

func TestCreate_ActionableErrorWrapsStderr(t *testing.T) {
	// Point the manager at a directory that is not a Git repository at all,
	// to force a git failure with stderr content to wrap.
	notARepo := t.TempDir()
	mgr := NewManager(notARepo)

	_, err := mgr.Create(context.Background(), "exec1", "issue-42", "HEAD")
	if err == nil {
		t.Fatal("Create against a non-repository should error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
		t.Errorf("error %q should include wrapped git stderr", err.Error())
	}
}
