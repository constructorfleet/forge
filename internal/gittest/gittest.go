// Package gittest provides shared test-only helpers for driving a real,
// temporary Git repository — the fixture internal/workspace,
// internal/engine, and cmd/forge's tests all need to exercise code that
// shells out to git without mocking it.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RunGit runs a git command against dir and fails the test on error,
// returning the command's combined stdout/stderr.
func RunGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// NewTempRepo creates a temporary Git repository with one commit on its
// default branch ("main") and returns its root path and that commit's SHA.
func NewTempRepo(t testing.TB) (root, initialSHA string) {
	t.Helper()
	root = t.TempDir()
	RunGit(t, root, "init", "-q", "-b", "main")
	RunGit(t, root, "config", "user.email", "test@example.com")
	RunGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	RunGit(t, root, "add", "README.md")
	RunGit(t, root, "commit", "-q", "-m", "initial")
	sha := strings.TrimSpace(RunGit(t, root, "rev-parse", "HEAD"))
	return root, sha
}
