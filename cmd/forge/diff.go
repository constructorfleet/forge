package main

import (
	"context"
	"fmt"
	"os/exec"
)

// gitDiffProducer implements engine.DiffProducer with the real git binary.
// Engine itself never shells out to git (see internal/engine's package doc
// comment); this is the seam cmd/forge implements it through, exactly as
// resolveBaseRevision/repoFromOrigin already do for other git operations.
type gitDiffProducer struct{}

// Diff runs `git diff base...HEAD` inside workspacePath and returns its
// output. The three-dot form diffs against the merge base rather than base
// itself, matching CONTEXT.md "Review"'s "diff (base...HEAD)" wording.
func (gitDiffProducer) Diff(ctx context.Context, workspacePath, base string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", base+"...HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("forge: git diff %s...HEAD in %s: %w", base, workspacePath, err)
	}
	return string(out), nil
}
