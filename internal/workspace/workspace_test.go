package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/gittest"
)

// runGit and newTempRepo delegate to internal/gittest, the shared fixture
// used by internal/engine and cmd/forge's tests too.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.RunGit(t, dir, args...)
}

func newTempRepo(t *testing.T) (root, initialSHA string) {
	t.Helper()
	return gittest.NewTempRepo(t)
}

// newManager is the test-side helper for NewManager, failing the test on a
// construction error (e.g. an invalid branch template) so call sites that
// don't care about that error stay concise.
func newManager(t *testing.T, repoRoot string, opts ...Option) *Manager {
	t.Helper()
	mgr, err := NewManager(repoRoot, opts...)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func TestCreate_WorktreeAtExpectedPathAndBranch(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manager resolves repoRoot's canonical form (e.g. macOS's /var ->
	// /private/var symlink) so its paths match what git itself reports;
	// resolve root the same way before comparing.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", root, err)
	}
	wantPath := filepath.Join(resolvedRoot, ".forge", "worktrees", "exec1", "issue-42")
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

	mgr := newManager(t, root)

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
	mgr := newManager(t, root)

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
	mgr := newManager(t, root)

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

func TestCreate_RejectsPathTraversalIDs(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

	cases := []struct {
		name        string
		executionID string
		issueID     string
	}{
		{"execution traversal", "../../etc", "issue-42"},
		{"issue traversal", "exec1", "../../etc"},
		{"execution dotdot only", "..", "issue-42"},
		{"issue dotdot only", "exec1", ".."},
		{"execution empty", "", "issue-42"},
		{"issue empty", "exec1", ""},
		{"execution path separator", "exec/1", "issue-42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.Create(context.Background(), tc.executionID, tc.issueID, base)
			if err == nil {
				t.Fatalf("Create(%q, %q) should have been rejected", tc.executionID, tc.issueID)
			}
		})
	}

	// Confirm no traversal attempt created anything above the intended
	// worktree root.
	worktreeRoot := filepath.Join(root, ".forge", "worktrees")
	if _, err := os.Stat(worktreeRoot); err == nil {
		entries, readErr := os.ReadDir(worktreeRoot)
		if readErr == nil && len(entries) != 0 {
			t.Errorf("worktree root %s should be empty after rejected Create calls, has: %v", worktreeRoot, entries)
		}
	}
}

func TestCleanup_RejectsPathTraversalIDs(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := newManager(t, root)

	if err := mgr.Cleanup(context.Background(), "../../etc", "issue-42"); err == nil {
		t.Fatal("Cleanup with a path-traversal executionID should be rejected")
	}
}

func TestValidate_RejectsPathTraversalIDs(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := newManager(t, root)

	if _, err := mgr.Validate(context.Background(), "exec1", ".."); err == nil {
		t.Fatal("Validate with a path-traversal issueID should be rejected")
	}
}

func TestCleanup_RemovesDirAndWorktreeEntry(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

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

func TestCleanup_DeletesBranch(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Cleanup(context.Background(), "exec1", "issue-42"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	branches := runGit(t, root, "branch", "--list", ws.Branch)
	if strings.TrimSpace(branches) != "" {
		t.Errorf("branch %s still exists after Cleanup: %q", ws.Branch, branches)
	}
}

func TestCleanup_MissingWorktreeIsNotAnError(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := newManager(t, root)

	if err := mgr.Cleanup(context.Background(), "exec1", "never-created"); err != nil {
		t.Errorf("Cleanup of nonexistent workspace should be a no-op, got: %v", err)
	}
}

func TestCreate_AfterCleanup_HonorsNewBase(t *testing.T) {
	root, base := newTempRepo(t)

	if err := os.WriteFile(filepath.Join(root, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	runGit(t, root, "add", "second.txt")
	runGit(t, root, "commit", "-q", "-m", "second")
	newerSHA := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	mgr := newManager(t, root)

	ws1, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	head1 := strings.TrimSpace(runGit(t, ws1.Path, "rev-parse", "HEAD"))
	if head1 != base {
		t.Fatalf("first Create HEAD = %s, want %s", head1, base)
	}

	if err := mgr.Cleanup(context.Background(), "exec1", "issue-42"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	ws2, err := mgr.Create(context.Background(), "exec1", "issue-42", newerSHA)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	head2 := strings.TrimSpace(runGit(t, ws2.Path, "rev-parse", "HEAD"))
	if head2 != newerSHA {
		t.Errorf("second Create HEAD = %s, want new base %s (branch was reused/stale instead of recreated)", head2, newerSHA)
	}
}

func TestValidate_ExistingWorktree(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := mgr.Validate(context.Background(), "exec1", "issue-42")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.Path != ws.Path || got.Branch != ws.Branch {
		t.Errorf("Validate Workspace = %+v, want %+v", got, ws)
	}
}

func TestValidate_MissingWorktree(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := newManager(t, root)

	_, err := mgr.Validate(context.Background(), "exec1", "never-created")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Validate err = %v, want ErrNotFound", err)
	}
}

func TestValidate_DirRemovedButWorktreeRegistered(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

	ws, err := mgr.Create(context.Background(), "exec1", "issue-42", base)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := os.RemoveAll(ws.Path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	_, err = mgr.Validate(context.Background(), "exec1", "issue-42")
	if err == nil {
		t.Fatal("Validate should not report a healthy worktree when its directory is missing")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("Validate should distinguish an unhealthy (registered) worktree from ErrNotFound")
	}
}

func TestCreate_ActionableErrorOnBadBase(t *testing.T) {
	root, _ := newTempRepo(t)
	mgr := newManager(t, root)

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
	mgr := newManager(t, notARepo)

	_, err := mgr.Create(context.Background(), "exec1", "issue-42", "HEAD")
	if err == nil {
		t.Fatal("Create against a non-repository should error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
		t.Errorf("error %q should include wrapped git stderr", err.Error())
	}
}

func TestNewManager_RejectsBranchTemplateMissingExecutionPlaceholder(t *testing.T) {
	root, _ := newTempRepo(t)

	_, err := NewManager(root, WithBranchTemplate("agent/{issue}"))
	if err == nil {
		t.Fatal("NewManager should reject a branch template lacking {execution}")
	}
}

func TestCreate_ConcurrentDistinctIssuesAllSucceed(t *testing.T) {
	root, base := newTempRepo(t)
	mgr := newManager(t, root)

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			issueID := "issue-" + string(rune('a'+i))
			_, err := mgr.Create(context.Background(), "exec1", issueID, base)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Create issue %d: %v", i, err)
		}
	}

	for i := 0; i < n; i++ {
		issueID := "issue-" + string(rune('a'+i))
		if _, err := mgr.Validate(context.Background(), "exec1", issueID); err != nil {
			t.Errorf("Validate issue %d after concurrent Create: %v", i, err)
		}
	}
}
