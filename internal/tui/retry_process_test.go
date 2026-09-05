package tui_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tui"
)

// writeStubScript writes an executable shell script under t.TempDir that
// prints body's stderr text and exits with code, so a test can drive
// ProcessRetrier.Retry against a fake child without spawning a real forge
// binary.
func writeStubScript(t *testing.T, stderr string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub script uses a POSIX shebang")
	}
	path := filepath.Join(t.TempDir(), "stub-forge")
	script := "#!/bin/sh\n"
	if stderr != "" {
		script += "echo " + stderr + " 1>&2\n"
	}
	script += "exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}

// TestProcessRetrierBuildsChildAtGitTopLevelWithAbsolutePaths proves the
// spawned child's Dir is the given repo root and its --config/--db flags
// carry the given absolute paths, whatever directory the TUI process itself
// runs from (#459).
func TestProcessRetrierBuildsChildAtGitTopLevelWithAbsolutePaths(t *testing.T) {
	cmd := tui.ProcessRetrier{
		RepoRoot:   "/repo/top",
		ConfigPath: "/repo/top/.forge.yaml",
		DBPath:     "/repo/top/.forge/forge.db",
		Executable: "/usr/bin/true",
	}.Command("ex-1", "#1")

	if cmd.Dir != "/repo/top" {
		t.Fatalf("Dir = %q, want the git top level", cmd.Dir)
	}
	want := []string{"/usr/bin/true", "retry", "ex-1/#1", "--config", "/repo/top/.forge.yaml", "--db", "/repo/top/.forge/forge.db"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("Args = %v, want %v", cmd.Args, want)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr = %+v, want Setpgid true (the process-group convention, so the child survives the TUI quitting)", cmd.SysProcAttr)
	}
}

// TestProcessRetrierCapturesStderrOnFailure proves a failing child's stderr
// is captured and surfaced as the error, so a refused retry is diagnosable
// (#458: some refreshRetryBase failures leave no trace in the store).
func TestProcessRetrierCapturesStderrOnFailure(t *testing.T) {
	stub := writeStubScript(t, "forge retry: base rebase conflict", 1)
	r := tui.ProcessRetrier{RepoRoot: t.TempDir(), ConfigPath: "/repo/.forge.yaml", DBPath: "/repo/.forge/forge.db", Executable: stub}

	result, err := r.Retry("ex-1", "#1")

	if err == nil {
		t.Fatal("Retry returned no error for a non-zero exit, want the exit surfaced")
	}
	if !strings.Contains(err.Error(), "base rebase conflict") {
		t.Fatalf("err = %v, want the child's stderr surfaced", err)
	}
	if !strings.Contains(result.Stderr, "base rebase conflict") {
		t.Fatalf("result.Stderr = %q, want the child's stderr captured", result.Stderr)
	}
	if result.ExitCode != 1 {
		t.Fatalf("result.ExitCode = %d, want 1", result.ExitCode)
	}
}

// TestProcessRetrierSucceedsOnZeroExit proves a clean child exit returns no
// error, so a successful retry is distinguishable from a failing one.
func TestProcessRetrierSucceedsOnZeroExit(t *testing.T) {
	stub := writeStubScript(t, "", 0)
	r := tui.ProcessRetrier{RepoRoot: t.TempDir(), ConfigPath: "/repo/.forge.yaml", DBPath: "/repo/.forge/forge.db", Executable: stub}

	_, err := r.Retry("ex-1", "#1")

	if err != nil {
		t.Fatalf("Retry returned %v for a zero exit, want nil", err)
	}
}

// TestProcessRetrierSpawnFailureIsDistinctFromAChildFailure proves a spawn
// that never starts (an unresolvable executable) is its own error, so a
// retry never attempted is distinguishable from one the child refused.
func TestProcessRetrierSpawnFailureIsDistinctFromAChildFailure(t *testing.T) {
	r := tui.ProcessRetrier{RepoRoot: t.TempDir(), ConfigPath: "/repo/.forge.yaml", DBPath: "/repo/.forge/forge.db", Executable: filepath.Join(t.TempDir(), "does-not-exist")}

	_, err := r.Retry("ex-1", "#1")

	if err == nil {
		t.Fatal("Retry returned no error for an unresolvable executable")
	}
	if strings.Contains(err.Error(), "exited") {
		t.Fatalf("err = %v, want a spawn failure, not a child exit", err)
	}
}
