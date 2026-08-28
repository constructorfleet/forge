package gate_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Teagan42/forge/internal/gate"
)

func TestExecCommandRunner_CapturesStdoutAndExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := gate.ExecCommandRunner{}

	exitCode, err := r.Run(context.Background(), t.TempDir(), "echo hello", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if got := stdout.String(); got != "hello\n" {
		t.Errorf("stdout = %q, want %q", got, "hello\n")
	}
}

func TestExecCommandRunner_NonZeroExitCodeNotAnError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := gate.ExecCommandRunner{}

	exitCode, err := r.Run(context.Background(), t.TempDir(), "exit 3", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v, want nil error for a plain non-zero exit", err)
	}
	if exitCode != 3 {
		t.Errorf("exitCode = %d, want 3", exitCode)
	}
}

func TestExecCommandRunner_RunsInWorkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("here\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	var stdout, stderr bytes.Buffer
	r := gate.ExecCommandRunner{}
	exitCode, err := r.Run(context.Background(), dir, "cat marker.txt", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if got := stdout.String(); got != "here\n" {
		t.Errorf("stdout = %q, want %q", got, "here\n")
	}
}

// TestExecCommandRunner_CommandNotFoundIsAPlainNonZeroExit documents that a
// nonexistent command run via `sh -c` is reported by the shell itself as a
// non-zero exit (conventionally 127), not a Go-level error: `sh` started
// fine, it just couldn't find the program to exec. Runner treats this
// exactly like any other failing gate.
func TestExecCommandRunner_CommandNotFoundIsAPlainNonZeroExit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := gate.ExecCommandRunner{}

	exitCode, err := r.Run(context.Background(), t.TempDir(), "definitely-not-a-real-command-xyz", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v, want nil error (the shell reports this as a non-zero exit)", err)
	}
	if exitCode == 0 {
		t.Error("exitCode = 0, want non-zero for a command that cannot be found")
	}
}
