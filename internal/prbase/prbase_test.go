package prbase_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/prbase"
	"github.com/Teagan42/forge/internal/storage"
)

// fakeStore serves one prerequisite Issue and Workspace, or ErrNotFound. A
// zero-value Issue or Workspace stands for "not recorded".
type fakeStore struct {
	issue   domain.Issue
	haveIss bool
	ws      domain.Workspace
	haveWS  bool
}

func (f fakeStore) GetIssue(_ context.Context, _, _ string) (domain.Issue, error) {
	if !f.haveIss {
		return domain.Issue{}, storage.ErrNotFound
	}
	return f.issue, nil
}

func (f fakeStore) WorkspaceByIssue(_ context.Context, _, _ string) (domain.Workspace, error) {
	if !f.haveWS {
		return domain.Workspace{}, storage.ErrNotFound
	}
	return f.ws, nil
}

func oneDep() domain.Issue {
	return domain.Issue{
		ID:           "child",
		Dependencies: []domain.Dependency{{IssueID: "child", DependsOnID: "parent"}},
	}
}

func TestResolveForNewPullRequestMergedPrerequisiteTargetsBaseBranch(t *testing.T) {
	// The 504 defect: a merged prerequisite (DONE) has its branch deleted, so
	// a new pull request must target the base branch, not the deleted branch.
	store := fakeStore{
		issue:   domain.Issue{ID: "parent", State: domain.StateDone},
		haveIss: true,
		ws:      domain.Workspace{IssueID: "parent", Branch: "forge/exec/parent"},
		haveWS:  true,
	}
	base, err := prbase.ResolveForNewPullRequest(context.Background(), store, "exec", oneDep(), "main")
	if err != nil {
		t.Fatalf("ResolveForNewPullRequest: %v", err)
	}
	if base != "main" {
		t.Fatalf("base = %q, want %q", base, "main")
	}
}

func TestResolveForNewPullRequestOpenPrerequisiteStacks(t *testing.T) {
	store := fakeStore{
		issue:   domain.Issue{ID: "parent", State: domain.StateCIPending},
		haveIss: true,
		ws:      domain.Workspace{IssueID: "parent", Branch: "forge/exec/parent"},
		haveWS:  true,
	}
	base, err := prbase.ResolveForNewPullRequest(context.Background(), store, "exec", oneDep(), "main")
	if err != nil {
		t.Fatalf("ResolveForNewPullRequest: %v", err)
	}
	if base != "forge/exec/parent" {
		t.Fatalf("base = %q, want %q", base, "forge/exec/parent")
	}
}

func TestResolveForNewPullRequestUnknownPrerequisiteTargetsBaseBranch(t *testing.T) {
	// An External prerequisite has no recorded Issue; it targets the base
	// branch, unchanged from Resolve's behavior.
	store := fakeStore{}
	base, err := prbase.ResolveForNewPullRequest(context.Background(), store, "exec", oneDep(), "main")
	if err != nil {
		t.Fatalf("ResolveForNewPullRequest: %v", err)
	}
	if base != "main" {
		t.Fatalf("base = %q, want %q", base, "main")
	}
}

func TestResolveForNewPullRequestNoDependenciesTargetsBaseBranch(t *testing.T) {
	store := fakeStore{}
	base, err := prbase.ResolveForNewPullRequest(context.Background(), store, "exec", domain.Issue{ID: "child"}, "main")
	if err != nil {
		t.Fatalf("ResolveForNewPullRequest: %v", err)
	}
	if base != "main" {
		t.Fatalf("base = %q, want %q", base, "main")
	}
}

func TestResolveKeepsStackedBaseForMergedPrerequisite(t *testing.T) {
	// Resolve (used by conflict resolution on an existing pull request) keeps
	// the recorded prerequisite branch even when the prerequisite is DONE.
	// This is the behavior ResolveForNewPullRequest deliberately does not
	// share.
	store := fakeStore{
		issue:   domain.Issue{ID: "parent", State: domain.StateDone},
		haveIss: true,
		ws:      domain.Workspace{IssueID: "parent", Branch: "forge/exec/parent"},
		haveWS:  true,
	}
	base, err := prbase.Resolve(context.Background(), store, "exec", oneDep(), "main")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if base != "forge/exec/parent" {
		t.Fatalf("base = %q, want %q", base, "forge/exec/parent")
	}
}
