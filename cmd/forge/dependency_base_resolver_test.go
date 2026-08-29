package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

// commitFile writes name with contents to dir's working tree and commits
// it, for building up a completed Dependency's resulting branch in these
// tests.
func commitFile(t *testing.T, dir, name, contents, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", message)
}

func TestDependencyBaseResolver_NoDependencies_ResolvesGitBaseTip(t *testing.T) {
	root, base := newTempRepo(t)
	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "1"})

	resolver := newCompletionResolver([]string{"1"}, nil, "HEAD")
	b := &dependencyBaseResolver{
		tracker:    trk,
		resolver:   resolver,
		workspaces: mustWorkspaceManager(t, root),
		repoRoot:   root,
		gitBase:    "HEAD",
	}

	got, err := b.CurrentBase(context.Background(), "1")
	if err != nil {
		t.Fatalf("CurrentBase: %v", err)
	}
	if got != base {
		t.Errorf("CurrentBase = %s, want git base tip %s", got, base)
	}
}

func TestDependencyBaseResolver_SingleManagedDependency_ResolvesToItsBranch(t *testing.T) {
	root, base := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	depWS, err := ws.Create(context.Background(), "exec-a", "a", base)
	if err != nil {
		t.Fatalf("Create dependency workspace: %v", err)
	}
	commitFile(t, depWS.Path, "a.txt", "from a\n", "issue-a work")

	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "1", Dependencies: []domain.Dependency{{IssueID: "1", DependsOnID: "a"}}})

	resolver := newCompletionResolver([]string{"1", "a"}, nil, "HEAD")
	resolver.onComplete("a", "exec-a", domain.StateReviewing, nil)

	b := &dependencyBaseResolver{tracker: trk, resolver: resolver, workspaces: ws, repoRoot: root, gitBase: "HEAD"}

	got, err := b.CurrentBase(context.Background(), "1")
	if err != nil {
		t.Fatalf("CurrentBase: %v", err)
	}
	if got != depWS.Branch {
		t.Errorf("CurrentBase = %s, want dependency branch %s", got, depWS.Branch)
	}

	// The dependent's Workspace, built on this base, must contain the
	// dependency's committed work.
	dependentWS, err := ws.Create(context.Background(), "exec-1", "1", got)
	if err != nil {
		t.Fatalf("Create dependent workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dependentWS.Path, "a.txt")); err != nil {
		t.Errorf("dependent workspace missing dependency's a.txt: %v", err)
	}
}

func TestDependencyBaseResolver_MultipleManagedDependencies_Integrates(t *testing.T) {
	root, base := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	wsA, err := ws.Create(context.Background(), "exec-a", "a", base)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	commitFile(t, wsA.Path, "a.txt", "from a\n", "issue-a work")

	wsB, err := ws.Create(context.Background(), "exec-b", "b", base)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	commitFile(t, wsB.Path, "b.txt", "from b\n", "issue-b work")

	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "c", Dependencies: []domain.Dependency{
		{IssueID: "c", DependsOnID: "a"},
		{IssueID: "c", DependsOnID: "b"},
	}})

	resolver := newCompletionResolver([]string{"a", "b", "c"}, nil, "HEAD")
	resolver.onComplete("a", "exec-a", domain.StateReviewing, nil)
	resolver.onComplete("b", "exec-b", domain.StateReviewing, nil)

	b := &dependencyBaseResolver{tracker: trk, resolver: resolver, workspaces: ws, repoRoot: root, gitBase: "HEAD"}

	got, err := b.CurrentBase(context.Background(), "c")
	if err != nil {
		t.Fatalf("CurrentBase: %v", err)
	}
	if got != "forge/integration/c" {
		t.Errorf("CurrentBase = %s, want forge/integration/c", got)
	}

	dependentWS, err := ws.Create(context.Background(), "exec-c", "c", got)
	if err != nil {
		t.Fatalf("Create dependent workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dependentWS.Path, "a.txt")); err != nil {
		t.Errorf("dependent workspace missing a's a.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dependentWS.Path, "b.txt")); err != nil {
		t.Errorf("dependent workspace missing b's b.txt: %v", err)
	}
}

func TestDependencyBaseResolver_ConflictingDependencies_SurfacesConflictError(t *testing.T) {
	root, base := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	wsA, err := ws.Create(context.Background(), "exec-a", "a", base)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	commitFile(t, wsA.Path, "README.md", "a edit\n", "issue-a conflicting edit")

	wsB, err := ws.Create(context.Background(), "exec-b", "b", base)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	commitFile(t, wsB.Path, "README.md", "b edit\n", "issue-b conflicting edit")

	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "c", Dependencies: []domain.Dependency{
		{IssueID: "c", DependsOnID: "a"},
		{IssueID: "c", DependsOnID: "b"},
	}})

	resolver := newCompletionResolver([]string{"a", "b", "c"}, nil, "HEAD")
	resolver.onComplete("a", "exec-a", domain.StateReviewing, nil)
	resolver.onComplete("b", "exec-b", domain.StateReviewing, nil)

	b := &dependencyBaseResolver{tracker: trk, resolver: resolver, workspaces: ws, repoRoot: root, gitBase: "HEAD"}

	_, err = b.CurrentBase(context.Background(), "c")
	if err == nil {
		t.Fatal("CurrentBase: want a conflict error, got nil")
	}
	var conflictErr *workspace.ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("CurrentBase err = %v (%T), want *workspace.ConflictError", err, err)
	}
}

func TestDependencyBaseResolver_UncompletedManagedDependency_Errors(t *testing.T) {
	root, _ := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "1", Dependencies: []domain.Dependency{{IssueID: "1", DependsOnID: "a"}}})

	resolver := newCompletionResolver([]string{"1", "a"}, nil, "HEAD")
	b := &dependencyBaseResolver{tracker: trk, resolver: resolver, workspaces: ws, repoRoot: root, gitBase: "HEAD"}

	if _, err := b.CurrentBase(context.Background(), "1"); err == nil {
		t.Fatal("CurrentBase: want error when dependency a has not completed, got nil")
	}
}

func TestDependencyBaseResolver_ExternalOnlyDependency_ResolvesGitBaseTip(t *testing.T) {
	root, base := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "1", Dependencies: []domain.Dependency{{IssueID: "1", DependsOnID: "external-99"}}})

	// "external-99" is not in the requested execution set, so it's External
	// (CONTEXT.md); completionResolver.externalSatisfied handles gating —
	// this resolver only needs to fall back to gitBase's tip, not consult
	// the checker itself.
	resolver := newCompletionResolver([]string{"1"}, nil, "HEAD")
	b := &dependencyBaseResolver{tracker: trk, resolver: resolver, workspaces: ws, repoRoot: root, gitBase: "HEAD"}

	got, err := b.CurrentBase(context.Background(), "1")
	if err != nil {
		t.Fatalf("CurrentBase: %v", err)
	}
	if got != base {
		t.Errorf("CurrentBase = %s, want git base tip %s", got, base)
	}
}

func TestDependencyBaseResolver_MixedManagedAndExternal_IntegratesWithBaseTip(t *testing.T) {
	root, base := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	wsA, err := ws.Create(context.Background(), "exec-a", "a", base)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	commitFile(t, wsA.Path, "a.txt", "from a\n", "issue-a work")

	trk := tracker.NewFakeTracker()
	trk.AddIssue(domain.Issue{ID: "1", Dependencies: []domain.Dependency{
		{IssueID: "1", DependsOnID: "a"},
		{IssueID: "1", DependsOnID: "external-99"},
	}})

	resolver := newCompletionResolver([]string{"1", "a"}, nil, "HEAD")
	resolver.onComplete("a", "exec-a", domain.StateReviewing, nil)
	b := &dependencyBaseResolver{tracker: trk, resolver: resolver, workspaces: ws, repoRoot: root, gitBase: "HEAD"}

	got, err := b.CurrentBase(context.Background(), "1")
	if err != nil {
		t.Fatalf("CurrentBase: %v", err)
	}
	if !strings.HasPrefix(got, "forge/integration/") {
		t.Errorf("CurrentBase = %s, want an integration branch (mixed managed+external sources)", got)
	}
}

func TestCompletionResolver_BranchFor_ResolvesCompletedIssuesBranch(t *testing.T) {
	root, _ := newTempRepo(t)
	ws := mustWorkspaceManager(t, root)

	r := newCompletionResolver([]string{"1"}, nil, "HEAD")
	if _, ok := r.branchFor("1", ws); ok {
		t.Fatal("branchFor before completion: want ok=false")
	}

	r.onComplete("1", "exec-1", domain.StateReviewing, nil)
	branch, ok := r.branchFor("1", ws)
	if !ok {
		t.Fatal("branchFor after completion: want ok=true")
	}
	if want := ws.BranchName("exec-1", "1"); branch != want {
		t.Errorf("branchFor = %s, want %s", branch, want)
	}
}
