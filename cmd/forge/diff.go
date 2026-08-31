package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// gitDiffProducer implements engine.DiffProducer with the real git binary.
// Engine itself never shells out to git (see internal/engine's package doc
// comment); this is the seam cmd/forge implements it through, exactly as
// resolveBaseRevision/repoFromOrigin already do for other git operations.
type gitDiffProducer struct{}

// Diff returns the publishable change set from base's merge base with HEAD
// through the current working tree. That preserves CONTEXT.md "Review"'s
// base...HEAD comparison for committed work while also including the staged,
// unstaged, and untracked files gitPublisher.Commit would publish later.
func (gitDiffProducer) Diff(ctx context.Context, workspacePath, base string) (string, error) {
	mergeBase, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "merge-base", base, "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("forge: git merge-base %s HEAD in %s: %w", base, workspacePath, err)
	}
	anchor := strings.TrimSpace(string(mergeBase))

	var diff bytes.Buffer
	out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", anchor).Output()
	if err != nil {
		return "", fmt.Errorf("forge: git diff %s in %s: %w", anchor, workspacePath, err)
	}
	diff.Write(out)

	untracked, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return "", fmt.Errorf("forge: git ls-files --others in %s: %w", workspacePath, err)
	}
	for _, path := range bytes.Split(bytes.TrimRight(untracked, "\x00"), []byte{0}) {
		if len(path) == 0 {
			continue
		}
		out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", "--no-index", "--", "/dev/null", string(path)).CombinedOutput()
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				return "", fmt.Errorf("forge: git diff --no-index /dev/null %s in %s: %w: %s", path, workspacePath, err, out)
			}
		}
		diff.Write(out)
	}
	return diff.String(), nil
}
