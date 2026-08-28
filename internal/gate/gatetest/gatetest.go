// Package gatetest provides a shared gate.CommandRunner test double, so
// internal/gate's own tests, internal/engine's integration tests, and
// ticket 21's retry-loop tests never shell out to a real tool. Mirrors
// internal/gittest: one fixture, used everywhere a fake CommandRunner is
// needed.
package gatetest

import (
	"context"
	"io"
	"sync"

	"github.com/Teagan42/forge/internal/gate"
)

var _ gate.CommandRunner = (*FakeCommandRunner)(nil)

// outcome is one programmed Run response for a given command string.
type outcome struct {
	exitCode int
	stdout   string
	stderr   string
	err      error
}

// FakeCommandRunner is a deterministic gate.CommandRunner double: outcomes
// are programmed per exact command string. Every call is recorded in call
// order, so tests can assert both what ran and what didn't (e.g. that a
// later gate was skipped after an earlier one failed).
type FakeCommandRunner struct {
	mu       sync.Mutex
	outcomes map[string]outcome
	calls    []string
}

// NewFakeCommandRunner returns an empty FakeCommandRunner. A command with
// no programmed outcome succeeds with exit code 0 and no output.
func NewFakeCommandRunner() *FakeCommandRunner {
	return &FakeCommandRunner{outcomes: map[string]outcome{}}
}

// ProgramResult sets the (exitCode, stdout, stderr) Run returns for
// command.
func (f *FakeCommandRunner) ProgramResult(command string, exitCode int, stdout, stderr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[command] = outcome{exitCode: exitCode, stdout: stdout, stderr: stderr}
}

// ProgramError sets the error Run returns for command, simulating the
// command failing to run at all (as opposed to running and exiting
// non-zero).
func (f *FakeCommandRunner) ProgramError(command string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[command] = outcome{err: err}
}

// Run records command and returns its programmed outcome (or a trivial
// success if none was programmed).
func (f *FakeCommandRunner) Run(_ context.Context, _, command string, stdout, stderr io.Writer) (int, error) {
	f.mu.Lock()
	oc, ok := f.outcomes[command]
	f.calls = append(f.calls, command)
	f.mu.Unlock()

	if !ok {
		return 0, nil
	}
	if oc.stdout != "" {
		_, _ = io.WriteString(stdout, oc.stdout)
	}
	if oc.stderr != "" {
		_, _ = io.WriteString(stderr, oc.stderr)
	}
	return oc.exitCode, oc.err
}

// Calls returns every command Run was called with, in call order.
func (f *FakeCommandRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
