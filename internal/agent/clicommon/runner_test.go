package clicommon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

func TestDefaultRunner_KillsWholeProcessGroupOnContextCancel(t *testing.T) {
	// A CLI agent may spawn children of its own; canceling ctx must kill
	// the whole process group, not just the direct child, or a stray
	// grandchild is left running and holding the Workspace worktree open
	// (issue 33, "Agent runs need a timeout").
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	runner := DefaultRunner("sh")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, _ = runner(ctx, dir, []string{"-c", "sleep 30 & echo $! > child.pid; wait"}, "", nil, func(string) {})
	}()

	pidPath := filepath.Join(dir, "child.pid")
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidPath)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("parse child pid: %v", err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child process never reported its pid")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not return after ctx cancellation")
	}

	// Give the kernel a moment to actually reap the signaled process.
	aliveDeadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(aliveDeadline) {
			t.Fatalf("child process %d still alive after parent's context was canceled", childPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
