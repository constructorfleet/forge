package gate

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// ExecCommandRunner is the production CommandRunner: it runs a gate's
// command as a real subprocess via `sh -c`, inside workDir. Tests inject a
// fake CommandRunner instead so they never shell out.
type ExecCommandRunner struct{}

var _ CommandRunner = ExecCommandRunner{}

// Run executes command inside workDir via `sh -c command`, streaming its
// stdout/stderr to the given writers. A plain non-zero exit is reported as
// (exitCode, nil) — that's an ordinary gate failure, not a Go error. err is
// reserved for the command failing to run at all (e.g. `sh` itself
// couldn't be started).
func (ExecCommandRunner) Run(ctx context.Context, workDir, command string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}
