package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/gittest"
)

func TestBaseBranchName_StripsOriginPrefixOnly(t *testing.T) {
	t.Parallel()

	if got := baseBranchName("origin/main"); got != "main" {
		t.Fatalf("baseBranchName(origin/main) = %q, want main", got)
	}
	if got := baseBranchName("upstream/main"); got != "upstream/main" {
		t.Fatalf("baseBranchName(upstream/main) = %q, want upstream/main", got)
	}
}

func TestGitPublisherCommit_CommitsDirtyWorkspaceAndReturnsHeadSHA(t *testing.T) {
	t.Parallel()

	root, _ := gittest.NewTempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}

	sha, err := (gitPublisher{}).Commit(context.Background(), execution.NewFakeEnvironment(domain.Workspace{Path: root}), "Add feature\n\nRefs #22")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	head := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "HEAD"))
	if sha != head {
		t.Fatalf("Commit returned %q, want HEAD %q", sha, head)
	}
	if got := strings.TrimSpace(gittest.RunGit(t, root, "log", "-1", "--pretty=%B")); got != "Add feature\n\nRefs #22" {
		t.Fatalf("commit message = %q", got)
	}
}

func TestGitPublisherCommit_CleanWorkspaceReturnsExistingHead(t *testing.T) {
	t.Parallel()

	root, base := gittest.NewTempRepo(t)

	sha, err := (gitPublisher{}).Commit(context.Background(), execution.NewFakeEnvironment(domain.Workspace{Path: root}), "unused")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if sha != base {
		t.Fatalf("Commit returned %q, want existing HEAD %q", sha, base)
	}
	if got := strings.TrimSpace(gittest.RunGit(t, root, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("commit count = %s, want 1", got)
	}
}

func TestGitPublisherPush_PushesBranchToOrigin(t *testing.T) {
	t.Parallel()

	origin := t.TempDir()
	gittest.RunGit(t, origin, "init", "--bare", "-q")

	root, _ := gittest.NewTempRepo(t)
	gittest.RunGit(t, root, "remote", "add", "origin", origin)
	gittest.RunGit(t, root, "checkout", "-q", "-b", "forge/exec/22")
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gittest.RunGit(t, root, "add", "feature.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "feature")

	if err := (gitPublisher{}).Push(context.Background(), execution.NewFakeEnvironment(domain.Workspace{Path: root}), "forge/exec/22"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if got := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")); got != "origin/forge/exec/22" {
		t.Fatalf("upstream = %q, want origin/forge/exec/22", got)
	}
	if got := strings.TrimSpace(gittest.RunGit(t, origin, "rev-parse", "refs/heads/forge/exec/22")); got == "" {
		t.Fatal("expected remote branch to exist")
	}
}

func TestGitPublisherForcePushWithLeaseRequiresExpectedRemoteSHA(t *testing.T) {
	t.Parallel()

	origin := t.TempDir()
	gittest.RunGit(t, origin, "init", "--bare", "-q")

	root, base := gittest.NewTempRepo(t)
	gittest.RunGit(t, root, "remote", "add", "origin", origin)
	gittest.RunGit(t, root, "checkout", "-q", "-b", "forge/exec/35")
	gittest.RunGit(t, root, "push", "-u", "origin", "forge/exec/35")

	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gittest.RunGit(t, root, "add", "feature.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "candidate")
	candidate := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "HEAD"))

	if err := (gitPublisher{}).ForcePushWithLease(context.Background(), root, "forge/exec/35", base); err != nil {
		t.Fatalf("ForcePushWithLease: %v", err)
	}
	if got := strings.TrimSpace(gittest.RunGit(t, origin, "rev-parse", "refs/heads/forge/exec/35")); got != candidate {
		t.Fatalf("remote head = %s, want candidate %s", got, candidate)
	}
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("second candidate\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gittest.RunGit(t, root, "add", "feature.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "second candidate")

	if err := (gitPublisher{}).ForcePushWithLease(context.Background(), root, "forge/exec/35", base); err == nil {
		t.Fatal("ForcePushWithLease with stale expected SHA succeeded, want lease failure")
	}
}

func TestGitPublisherForcePushCommitWithLeaseRestoresSpecificCommit(t *testing.T) {
	t.Parallel()

	origin := t.TempDir()
	gittest.RunGit(t, origin, "init", "--bare", "-q")

	root, original := gittest.NewTempRepo(t)
	gittest.RunGit(t, root, "remote", "add", "origin", origin)
	gittest.RunGit(t, root, "checkout", "-q", "-b", "forge/exec/36")
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gittest.RunGit(t, root, "add", "feature.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "candidate")
	candidate := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "HEAD"))
	gittest.RunGit(t, root, "push", "-u", "origin", "forge/exec/36")

	if err := (gitPublisher{}).ForcePushCommitWithLease(context.Background(), root, "forge/exec/36", original, candidate); err != nil {
		t.Fatalf("ForcePushCommitWithLease: %v", err)
	}
	if got := strings.TrimSpace(gittest.RunGit(t, origin, "rev-parse", "refs/heads/forge/exec/36")); got != original {
		t.Fatalf("remote head = %s, want original %s", got, original)
	}
	if err := (gitPublisher{}).ForcePushCommitWithLease(context.Background(), root, "forge/exec/36", candidate, candidate); err == nil {
		t.Fatal("ForcePushCommitWithLease with stale expected SHA succeeded, want lease failure")
	}
}

func TestGitPublisherReset_RestoresWorkspaceToCommit(t *testing.T) {
	t.Parallel()

	root, base := gittest.NewTempRepo(t)
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gittest.RunGit(t, root, "add", "feature.txt")
	gittest.RunGit(t, root, "commit", "-q", "-m", "candidate")

	if err := (gitPublisher{}).Reset(context.Background(), root, base); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	head := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "HEAD"))
	if head != base {
		t.Fatalf("HEAD = %s, want %s", head, base)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("feature.txt stat err = %v, want file removed by reset", err)
	}
}
