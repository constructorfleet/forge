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
//
// `git diff` only ever reflects committed work — it diffs base against
// HEAD, not the working tree. Today's implementation Agent never commits
// (ticket 22 owns that), so until ticket 22 lands, a real Reviewer wired up
// ahead of it would see an empty diff here regardless of what the Agent
// actually changed on disk. Ticket 22's commit step must run (or this
// producer must otherwise account for uncommitted changes) before a
// production Reviewer can see real content.
func (gitDiffProducer) Diff(ctx context.Context, workspacePath, base string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", base+"...HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("forge: git diff %s...HEAD in %s: %w", base, workspacePath, err)
	}
	return string(out), nil
}
