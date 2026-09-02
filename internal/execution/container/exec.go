package container

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// CommandRunner runs one CLI invocation (a docker or podman subcommand) and
// returns its captured stdout/stderr and exit code. Real invocations run
// through ExecCommandRunner; tests inject a fake CommandRunner instead so
// they never shell out to a container daemon.
type CommandRunner interface {
	// Run executes args (the CLI binary plus its arguments) directly, with
	// no shell interpretation, forwarding stdin to the process's standard
	// input when non-empty. It returns the process's exit code; err is
	// reserved for a failure to even start the process (e.g. the binary is
	// not on PATH), which callers treat as the CLI runtime being
	// unavailable rather than an ordinary command failure.
	Run(ctx context.Context, args []string, stdin string) (stdout, stderr string, exitCode int, err error)
}

// ExecCommandRunner is the production CommandRunner: it runs args as a real
// subprocess, with no shell involved. Tests inject a fake CommandRunner
// instead so they never shell out to a container daemon.
type ExecCommandRunner struct{}

var _ CommandRunner = ExecCommandRunner{}

// Run executes args directly via exec.CommandContext, forwarding stdin and
// capturing stdout/stderr. A plain non-zero exit is reported as
// (stdout, stderr, exitCode, nil) — that's an ordinary CLI failure, not a Go
// error. err is reserved for the process failing to start at all (e.g. the
// binary does not exist).
func (ExecCommandRunner) Run(ctx context.Context, args []string, stdin string) (string, string, int, error) {
	if len(args) == 0 {
		return "", "", -1, errors.New("container: ExecCommandRunner: empty args")
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.WaitDelay = execWaitDelay
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	return stdout.String(), stderr.String(), -1, err
}
