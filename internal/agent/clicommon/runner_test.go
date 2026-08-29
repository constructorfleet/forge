package clicommon

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestDefaultRunner_CapturesStdoutStderrAndExitCode(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	runner := DefaultRunner("sh")
	var lines []string
	stdout, stderr, exitCode, err := runner(
		context.Background(), t.TempDir(),
		[]string{"-c", "echo out-line; echo err-line 1>&2; exit 3"},
		"", nil,
		func(line string) { lines = append(lines, line) },
	)
	if err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
	if exitCode != 3 {
		t.Fatalf("exitCode = %d, want 3", exitCode)
	}
	if !strings.Contains(stdout, "out-line") {
		t.Fatalf("stdout = %q, want out-line", stdout)
	}
	if !strings.Contains(stderr, "err-line") {
		t.Fatalf("stderr = %q, want err-line", stderr)
	}
	if len(lines) != 1 || lines[0] != "out-line" {
		t.Fatalf("onLine lines = %v, want [out-line]", lines)
	}
}

func TestDefaultRunner_WritesStdinToSubprocess(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}

	runner := DefaultRunner("cat")
	stdout, _, exitCode, err := runner(context.Background(), t.TempDir(), nil, "hello from stdin", nil, func(string) {})
	if err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "hello from stdin") {
		t.Fatalf("stdout = %q, want it to echo stdin", stdout)
	}
}

func TestDefaultRunner_BoundsUnboundedOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if _, err := exec.LookPath("yes"); err != nil {
		t.Skip("yes not available")
	}

	runner := DefaultRunner("sh")
	stdout, _, _, err := runner(context.Background(), t.TempDir(), []string{"-c", "yes x | head -c 5000000"}, "", nil, func(string) {})
	if err != nil {
		t.Fatalf("runner returned error: %v", err)
	}
	if len(stdout) > MaxCapturedOutputLen*2 {
		t.Fatalf("captured stdout len = %d, want bounded near MaxCapturedOutputLen (%d)", len(stdout), MaxCapturedOutputLen)
	}
}
