package container_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/execution/container"
)

func TestExecCommandRunner_CapturesStdoutAndExitCode(t *testing.T) {
	r := container.ExecCommandRunner{}

	stdout, _, exitCode, err := r.Run(context.Background(), []string{"echo", "hello"}, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", stdout, "hello\n")
	}
}

func TestExecCommandRunner_NonZeroExitCodeNotAnError(t *testing.T) {
	r := container.ExecCommandRunner{}

	_, _, exitCode, err := r.Run(context.Background(), []string{"sh", "-c", "exit 3"}, "")
	if err != nil {
		t.Fatalf("Run: %v, want nil error for a plain non-zero exit", err)
	}
	if exitCode != 3 {
		t.Errorf("exitCode = %d, want 3", exitCode)
	}
}

func TestExecCommandRunner_ForwardsStdin(t *testing.T) {
	r := container.ExecCommandRunner{}

	stdout, _, exitCode, err := r.Run(context.Background(), []string{"cat"}, "hi there")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if stdout != "hi there" {
		t.Errorf("stdout = %q, want %q", stdout, "hi there")
	}
}

func TestExecCommandRunner_MissingBinaryIsAnError(t *testing.T) {
	r := container.ExecCommandRunner{}

	_, _, _, err := r.Run(context.Background(), []string{"forge-container-runtime-test-no-such-binary"}, "")
	if err == nil {
		t.Fatal("Run: want an error for a binary that does not exist, got nil")
	}
}
