package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
)

// fakeAncestorChecker is a fixed-answer engine.AncestorChecker double: the
// retry-base-refresh tests care about how RetryIssue reacts to the
// ancestry verdict, not about exercising real `git merge-base
// --is-ancestor` (internal/workspace's own tests already cover that
// primitive via the production checker in cmd/forge).
type fakeAncestorChecker struct {
	ok  bool
	err error
}

func (f fakeAncestorChecker) IsAncestor(context.Context, string, string) (bool, error) {
	return f.ok, f.err
}

var _ engine.AncestorChecker = fakeAncestorChecker{}

// advanceTarget commits an unrelated, empty commit onto repoRoot's current
// branch (simulating another Issue's PR merging into the target branch
// while this one sat FAILED) and returns the new tip SHA.
func advanceTarget(t *testing.T, repoRoot, message string) string {
	t.Helper()
	gittest.RunGit(t, repoRoot, "commit", "--allow-empty", "-q", "-m", message)
	return strings.TrimSpace(gittest.RunGit(t, repoRoot, "rev-parse", "HEAD"))
}

// TestRetryIssue_RefreshesBaseWhenTargetAdvanced is ticket 29's first
// acceptance case: a retry after the target branch advanced re-bases the
// Worker onto the new tip and re-gates against it (the existing repair
// pipeline this reruns already covers "full gate set").
func TestRetryIssue_RefreshesBaseWhenTargetAdvanced(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"70": {ID: "70", Title: "Refresh base"},
	})
	te.fake.ProgramResult("70", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})
	te.fake.ProgramResult("70", agent.AgentResult{Status: agent.StatusImplemented, Summary: "fixed"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "70", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	newTip := advanceTarget(t, te.eng.RepoRoot, "unrelated advance")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: true}

	retried, err := te.eng.RetryIssue(ctx, result.ExecutionID, "70")
	if err != nil {
		t.Fatalf("RetryIssue: %v", err)
	}
	if retried.State != domain.StateCommitting {
		t.Fatalf("retried state = %s, want COMMITTING", retried.State)
	}

	events, err := te.store.EventsByIssue(ctx, result.ExecutionID, "70")
	if err != nil {
		t.Fatalf("EventsByIssue: %v", err)
	}
	var sawRefresh bool
	for _, e := range events {
		if e.Type == "worker.base_captured" && strings.Contains(e.Data, newTip) {
			sawRefresh = true
		}
	}
	if !sawRefresh {
		t.Fatalf("no worker.base_captured event recorded for refreshed base %s; events=%+v", newTip, events)
	}

	ws, err := te.store.WorkspaceByIssue(ctx, result.ExecutionID, "70")
	if err != nil {
		t.Fatalf("WorkspaceByIssue: %v", err)
	}
	head := strings.TrimSpace(gittest.RunGit(t, ws.Path, "rev-parse", "HEAD"))
	if head != newTip {
		t.Fatalf("workspace HEAD = %s, want refreshed base %s", head, newTip)
	}
}

// TestRetryIssue_RefusesBaseRefreshThatWouldDropMergedDependency confirms a
// refresh is refused, not silently applied, when Ancestry reports the new
// tip does not descend from the previously captured base — preserving
// "never branch from a base that predates a dependency's merge"
// (ADR 0005/0006). The Issue is left exactly as it was: still FAILED, retry
// budget untouched.
func TestRetryIssue_RefusesBaseRefreshThatWouldDropMergedDependency(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"71": {ID: "71", Title: "Refuse divergent refresh"},
	})
	te.fake.ProgramResult("71", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "71", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	newTip := advanceTarget(t, te.eng.RepoRoot, "diverged advance")
	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: false}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "71")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want refusal error")
	}
	var conflictErr *engine.RebaseConflictError
	if errors.As(err, &conflictErr) {
		t.Fatalf("RetryIssue err = %v, want a refusal error, not a RebaseConflictError", err)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "71")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateFailed {
		t.Fatalf("issue state after refused refresh = %s, want unchanged FAILED", issue.State)
	}
}

// TestRetryIssue_RebaseConflictReportedDistinctly confirms a rebase
// conflict onto the refreshed tip surfaces as a *RebaseConflictError naming
// the conflicting paths, not a generic error, and leaves the Issue FAILED
// rather than advancing it.
func TestRetryIssue_RebaseConflictReportedDistinctly(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"72": {ID: "72", Title: "Conflict on refresh"},
	})
	te.fake.ProgramResult("72", agent.AgentResult{Status: agent.StatusFailed, Summary: "boom"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "72", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("initial state = %s, want FAILED", result.Issue.State)
	}

	ws, err := te.store.WorkspaceByIssue(ctx, result.ExecutionID, "72")
	if err != nil {
		t.Fatalf("WorkspaceByIssue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path, "README.md"), []byte("worker edit\n"), 0o644); err != nil {
		t.Fatalf("write README.md in workspace: %v", err)
	}
	gittest.RunGit(t, ws.Path, "add", "README.md")
	gittest.RunGit(t, ws.Path, "commit", "-q", "-m", "worker edit")

	if err := os.WriteFile(filepath.Join(te.eng.RepoRoot, "README.md"), []byte("conflicting target edit\n"), 0o644); err != nil {
		t.Fatalf("write README.md on target: %v", err)
	}
	gittest.RunGit(t, te.eng.RepoRoot, "add", "README.md")
	newTip := advanceTarget(t, te.eng.RepoRoot, "conflicting target edit")

	te.eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return newTip, nil
	})
	te.eng.Ancestry = fakeAncestorChecker{ok: true}

	_, err = te.eng.RetryIssue(ctx, result.ExecutionID, "72")
	if err == nil {
		t.Fatal("RetryIssue err = nil, want *RebaseConflictError")
	}
	var conflictErr *engine.RebaseConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("RetryIssue err = %v (%T), want *RebaseConflictError", err, err)
	}
	if conflictErr.Base != newTip {
		t.Errorf("RebaseConflictError.Base = %s, want %s", conflictErr.Base, newTip)
	}
	if len(conflictErr.Paths) != 1 || conflictErr.Paths[0] != "README.md" {
		t.Errorf("RebaseConflictError.Paths = %v, want [README.md]", conflictErr.Paths)
	}

	issue, err := te.store.GetIssue(ctx, result.ExecutionID, "72")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateFailed {
		t.Fatalf("issue state after rebase conflict = %s, want unchanged FAILED", issue.State)
	}
}
