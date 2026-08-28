package gate

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// waitDelay bounds how long Run waits, after the `sh` process itself has
// exited, for the io-copy goroutines feeding stdout/stderr to see EOF. It
// exists because cmd.Stdout/cmd.Stderr here are arbitrary io.Writers (not
// *os.File): exec.Cmd services them via an OS pipe plus a background copy
// goroutine, and Cmd.Wait ordinarily blocks until that goroutine sees EOF —
// which a backgrounded grandchild that inherited the write end of the pipe
// can indefinitely prevent, even though the direct child (and the command
// it ran) is long gone. Without a bound, one gate command with a stray
// background process could hang the whole orchestration run. See
// https://pkg.go.dev/os/exec#Cmd.WaitDelay.
const waitDelay = 2 * time.Second

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
	cmd.WaitDelay = waitDelay

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
