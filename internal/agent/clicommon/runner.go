package clicommon

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/Teagan42/forge/internal/textcap"
)

// MaxCapturedOutputLen bounds how much of stdout/stderr DefaultRunner ever
// holds in memory at once, independent of MaxDiagnosticLen's
// presentation-time truncation: a small multiple keeps some headroom for
// pre-truncation formatting while still guaranteeing a runaway subprocess
// can't force unbounded memory use.
const MaxCapturedOutputLen = 4 * MaxDiagnosticLen

// Runner executes one CLI backend invocation: args are passed on the
// command line, stdin carries the prompt, dir is the working directory
// (the Issue's Workspace), and env is the fully-resolved (already
// sanitized) environment for the subprocess. Implementations must honor
// ctx cancellation by killing the subprocess. Tests inject a fake Runner so
// they never shell out to a real binary; production code uses
// DefaultRunner. Mirrors internal/agent/claude.Runner so every CLI Agent
// Adapter shares one contract.
//
// onLine, when non-nil, is invoked with each stdout line as it is produced,
// letting a caller parse/persist a transcript incrementally. The returned
// stdout is still the full (bounded) capture for diagnostics and fallback
// parsing.
type Runner func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(line string)) (stdout, stderr string, exitCode int, err error)

// DefaultRunner returns the Runner used in production for a CLI backend: it
// execs executable via exec.CommandContext, so ctx cancellation kills the
// subprocess. Mirrors internal/agent/claude's defaultRunner, generalized so
// every CLI Agent Adapter shares one implementation.
func DefaultRunner(executable string) Runner {
	return func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)

		// Bounded, tail-preserving writers (internal/textcap): a runaway
		// subprocess must not let Forge hold arbitrarily large output in
		// memory. stdout is still captured in full (bounded) for
		// diagnostics and non-stream fallback; onLine additionally sees
		// each line live as it is produced.
		stdoutTail := textcap.NewTailWriter(MaxCapturedOutputLen)
		stderr := textcap.NewTailWriter(MaxCapturedOutputLen)
		cmd.Stderr = stderr

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return "", stderr.String(), -1, err
		}
		if err := cmd.Start(); err != nil {
			return "", stderr.String(), -1, err
		}

		// Read stdout to EOF before Wait (required by StdoutPipe's
		// contract), delivering each line to onLine as it arrives and
		// mirroring it into the bounded tail. bufio.Reader.ReadString grows
		// to hold an arbitrarily long single line, unlike bufio.Scanner's
		// fixed token cap.
		reader := bufio.NewReader(stdoutPipe)
		for {
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				_, _ = stdoutTail.Write([]byte(line))
				if onLine != nil {
					onLine(strings.TrimRight(line, "\r\n"))
				}
			}
			if readErr != nil {
				break
			}
		}

		err = cmd.Wait()
		exitCode := 0
		if err != nil {
			// An *exec.ExitError means the subprocess ran and exited
			// non-zero: a normal, diagnosable outcome carried via
			// exitCode, not a transport-level failure. Any other error
			// means the subprocess could not be run at all, or was killed
			// (e.g. by context cancellation via exec.CommandContext), and
			// is surfaced as err.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
				err = nil
			}
		}
		return stdoutTail.String(), stderr.String(), exitCode, err
	}
}
