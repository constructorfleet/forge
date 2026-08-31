package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Teagan42/forge/internal/repolock"
)

// gitPublisher implements engine.Publisher with the real git binary. Engine
// itself never shells out to git (see internal/engine's package doc
// comment); this is the seam cmd/forge implements it through, exactly as
// gitDiffProducer does for the REVIEWING stage's diff.
type gitPublisher struct {
	locks *repolock.Locker
}

// Commit stages every change in workspacePath and, if that leaves anything
// staged, commits it with message. A Workspace with nothing to commit
// (e.g. a retried run resuming after a prior successful commit) is not an
// error: Commit is then a no-op and simply returns the current HEAD SHA.
func (p gitPublisher) Commit(ctx context.Context, workspacePath, message string) (string, error) {
	if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "add", "-A").CombinedOutput(); err != nil {
		return "", fmt.Errorf("forge: git add -A in %s: %w: %s", workspacePath, err, out)
	}

	dirty, err := hasStagedChanges(ctx, workspacePath)
	if err != nil {
		return "", err
	}
	if dirty {
		if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "commit", "-m", message).CombinedOutput(); err != nil {
			return "", fmt.Errorf("forge: git commit in %s: %w: %s", workspacePath, err, out)
		}
	}

	out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("forge: git rev-parse HEAD in %s: %w", workspacePath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// hasStagedChanges reports whether workspacePath has any staged changes,
// via `git diff --cached --quiet`'s exit code (0 = clean, 1 = dirty; any
// other outcome is a real error).
func hasStagedChanges(ctx context.Context, workspacePath string) (bool, error) {
	err := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", "--cached", "--quiet").Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("forge: git diff --cached --quiet in %s: %w", workspacePath, err)
}

// Push pushes branch to the "origin" remote, creating it there if it
// doesn't already exist (-u sets up branch's upstream tracking, matching
// what a subsequent PR needs). Pushing a branch whose remote tip already
// matches the local branch succeeds as a no-op, so no separate existence
// check is needed for idempotency.
func (p gitPublisher) Push(ctx context.Context, workspacePath, branch string) error {
	push := func() error {
		if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "push", "-u", "origin", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("forge: git push -u origin %s in %s: %w: %s", branch, workspacePath, err, out)
		}
		return nil
	}
	if p.locks == nil {
		return push()
	}
	return p.locks.WithLock(ctx, "branch:"+branch, push)
}

// ForcePush implements ci.BranchPusher with the real git binary, force-
// pushing branch to the "origin" remote after internal/workspace's Rebase
// has moved its tip non-fast-forward (issue 233: a stale pull request
// rebased onto its target branch). --force-with-lease rather than a bare
// --force: it aborts instead of clobbering if some other process moved the
// remote branch since Forge last observed it, so a concurrent push to the
// same branch is a safe failure here, not silently lost work.
func (p gitPublisher) ForcePush(ctx context.Context, workspacePath, branch string) error {
	push := func() error {
		if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "push", "--force-with-lease", "-u", "origin", branch).CombinedOutput(); err != nil {
			return fmt.Errorf("forge: git push --force-with-lease -u origin %s in %s: %w: %s", branch, workspacePath, err, out)
		}
		return nil
	}
	if p.locks == nil {
		return push()
	}
	return p.locks.WithLock(ctx, "branch:"+branch, push)
}

// EnsureWorkspaceReady verifies workspacePath has no uncommitted changes
// and no in-progress Git operation before an automatic conflict-repair path
// performs destructive branch movement.
func (p gitPublisher) EnsureWorkspaceReady(ctx context.Context, workspacePath string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("forge: git status --porcelain in %s: %w", workspacePath, err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("workspace has uncommitted changes")
	}
	for _, name := range []string{"MERGE_HEAD", "REBASE_HEAD", "CHERRY_PICK_HEAD", "rebase-merge", "rebase-apply"} {
		gitPathOut, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "rev-parse", "--git-path", name).Output()
		if err != nil {
			return fmt.Errorf("forge: git rev-parse --git-path %s in %s: %w", name, workspacePath, err)
		}
		gitPath := strings.TrimSpace(string(gitPathOut))
		if gitPath == "" {
			continue
		}
		if !filepath.IsAbs(gitPath) {
			gitPath = filepath.Join(workspacePath, gitPath)
		}
		if _, err := os.Stat(gitPath); err == nil {
			return fmt.Errorf("workspace has in-progress git operation: %s", name)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect git operation marker %s: %w", name, err)
		}
	}
	return nil
}

// ForcePushWithLease publishes workspacePath's current HEAD to branch only
// when origin/branch still equals expectedRemoteSHA.
func (p gitPublisher) ForcePushWithLease(ctx context.Context, workspacePath, branch, expectedRemoteSHA string) error {
	push := func() error {
		lease := "--force-with-lease=refs/heads/" + branch + ":" + expectedRemoteSHA
		refspec := "HEAD:" + branch
		if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "push", lease, "-u", "origin", refspec).CombinedOutput(); err != nil {
			return fmt.Errorf("forge: git push %s -u origin %s in %s: %w: %s", lease, refspec, workspacePath, err, out)
		}
		return nil
	}
	if p.locks == nil {
		return push()
	}
	return p.locks.WithLock(ctx, "branch:"+branch, push)
}

// ForcePushCommitWithLease restores branch to commitSHA only when
// origin/branch still equals expectedRemoteSHA.
func (p gitPublisher) ForcePushCommitWithLease(ctx context.Context, workspacePath, branch, commitSHA, expectedRemoteSHA string) error {
	push := func() error {
		lease := "--force-with-lease=refs/heads/" + branch + ":" + expectedRemoteSHA
		refspec := commitSHA + ":" + branch
		if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "push", lease, "origin", refspec).CombinedOutput(); err != nil {
			return fmt.Errorf("forge: git push %s origin %s in %s: %w: %s", lease, refspec, workspacePath, err, out)
		}
		return nil
	}
	if p.locks == nil {
		return push()
	}
	return p.locks.WithLock(ctx, "branch:"+branch, push)
}

// Reset implements ci.BranchResetter, restoring a Forge-owned Workspace
// branch to the pull request head recorded before an automatic conflict
// repair candidate was attempted.
func (p gitPublisher) Reset(ctx context.Context, workspacePath, commitSHA string) error {
	reset := func() error {
		if out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "reset", "--hard", commitSHA).CombinedOutput(); err != nil {
			return fmt.Errorf("forge: git reset --hard %s in %s: %w: %s", commitSHA, workspacePath, err, out)
		}
		return nil
	}
	if p.locks == nil {
		return reset()
	}
	return p.locks.WithLock(ctx, "workspace:"+workspacePath, reset)
}

// baseBranchName strips a "origin/" remote prefix from a configured
// cfg.Git.Base (e.g. "origin/main" -> "main"), since buildTracker/
// buildEngine always resolve the tracked repository from the "origin"
// remote (see repoFromOrigin) and a pull request's base must be a plain
// branch name, not a remote-qualified ref. A base without that prefix
// (e.g. a bare "main") is returned unchanged.
func baseBranchName(base string) string {
	return strings.TrimPrefix(base, "origin/")
}
