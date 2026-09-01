package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestStaleBinaryWarning_SHAMismatch confirms staleBinaryWarning reports a
// warning when the running binary's embedded commit doesn't match the
// repo's current HEAD — the issue #321 scenario where a fixed source
// commit lands but the installed binary predates it.
func TestStaleBinaryWarning_SHAMismatch(t *testing.T) {
	dir := initGitFixture(t)
	commitFile(t, dir, "README.md", "first commit", "first commit")
	head := currentHead(t, dir)

	msg, stale := staleBinaryWarning(dir, "0000000000000000000000000000000000000000")
	if !stale {
		t.Fatalf("expected staleBinaryWarning to report stale, got not stale (head=%s)", head)
	}
	if !strings.Contains(msg, head) {
		t.Errorf("warning should mention the repo HEAD %s, got: %s", head, msg)
	}
}

// TestStaleBinaryWarning_SHAMatch confirms no warning fires when the
// embedded commit matches the repo's current HEAD.
func TestStaleBinaryWarning_SHAMatch(t *testing.T) {
	dir := initGitFixture(t)
	commitFile(t, dir, "README.md", "first commit", "first commit")
	head := currentHead(t, dir)

	_, stale := staleBinaryWarning(dir, head)
	if stale {
		t.Fatalf("expected staleBinaryWarning to report not stale when commits match")
	}
}

// TestStaleBinaryWarning_UnstampedBinary confirms an unstamped binary
// ("unknown", the default for a plain `go build`) is never reported stale —
// there's nothing to compare against.
func TestStaleBinaryWarning_UnstampedBinary(t *testing.T) {
	dir := initGitFixture(t)
	commitFile(t, dir, "README.md", "first commit", "first commit")

	_, stale := staleBinaryWarning(dir, "unknown")
	if stale {
		t.Fatalf("expected staleBinaryWarning to skip an unstamped binary")
	}
}

func currentHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}
