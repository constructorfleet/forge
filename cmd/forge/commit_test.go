package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	sha, err := (gitPublisher{}).Commit(context.Background(), root, "Add feature\n\nRefs #22")
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

	sha, err := (gitPublisher{}).Commit(context.Background(), root, "unused")
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

	if err := (gitPublisher{}).Push(context.Background(), root, "forge/exec/22"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if got := strings.TrimSpace(gittest.RunGit(t, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")); got != "origin/forge/exec/22" {
		t.Fatalf("upstream = %q, want origin/forge/exec/22", got)
	}
	if got := strings.TrimSpace(gittest.RunGit(t, origin, "rev-parse", "refs/heads/forge/exec/22")); got == "" {
		t.Fatal("expected remote branch to exist")
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
