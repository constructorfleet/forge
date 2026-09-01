package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// staleBinaryWarning compares this binary's embedded buildCommit against
// repoRoot's current git HEAD (issue #321's optional guard). It reports
// stale whenever the two are known and differ, so a source fix that landed
// after the binary was built doesn't silently go unused in the dogfood
// loop.
//
// A binary built without ldflags embeds "unknown" (see version.go); there
// is nothing to compare in that case, so staleBinaryWarning always reports
// not-stale rather than warning on every single run. A repoRoot that isn't
// a git checkout (git rev-parse fails) is treated the same way: this is a
// best-effort diagnostic, not a hard requirement.
func staleBinaryWarning(repoRoot, embeddedCommit string) (string, bool) {
	if embeddedCommit == "" || embeddedCommit == "unknown" {
		return "", false
	}

	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false
	}
	head := strings.TrimSpace(string(out))
	if head == "" || head == embeddedCommit || strings.HasPrefix(head, embeddedCommit) {
		return "", false
	}

	return fmt.Sprintf(
		"forge: warning: this binary was built from commit %s but the repo at %s is now at %s; "+
			"rebuild with `go build -o $(which forge) ./cmd/forge` before relying on recent source changes",
		embeddedCommit, repoRoot, head,
	), true
}
