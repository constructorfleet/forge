// Package gate implements Forge's Gate Runner (CONTEXT.md "Gate Runner"):
// it executes an Issue's configured Quality Gates in order, captures each
// one's result, and produces bounded diagnostic feedback for the Agent when
// a gate fails. Quality Gates are configured, never discovered — Runner
// only ever executes the config.QualityGate commands it is given.
package gate

import (
	"context"
	"io"
	"time"

	"github.com/Teagan42/forge/internal/config"
)

// CommandRunner executes one Quality Gate's command inside workDir,
// streaming its stdout/stderr to the given writers as the command runs —
// so output can be bounded at the source (see boundedWriter) rather than
// truncated after the fact. It returns the process's exit code; err is
// reserved for a failure to even run the command (e.g. the shell itself
// could not start), which Runner treats as a failing gate rather than
// aborting the whole run.
//
// Real gate commands run as subprocesses via ExecCommandRunner; tests
// inject a fake so they never shell out.
type CommandRunner interface {
	Run(ctx context.Context, workDir, command string, stdout, stderr io.Writer) (exitCode int, err error)
}

// Result is one executed Quality Gate's outcome. See CONTEXT.md
// "Gate Runner": name, command, timing, exit code, and captured (bounded)
// stdout/stderr.
type Result struct {
	Name       string
	Command    string
	StartedAt  time.Time
	FinishedAt time.Time
	ExitCode   int
	Stdout     string
	Stderr     string
	Passed     bool
}

// Options configures a Runner.Run call.
type Options struct {
	// MaxOutputBytes bounds each gate's captured stdout/stderr stream,
	// tail-preserving (IDEATION.md §23 "Output bounding"). <= 0 means
	// unbounded.
	MaxOutputBytes int

	// ContinueOnFailure, if true, runs every configured gate regardless of
	// earlier failures. The zero value is stop-on-first-fail, the default
	// per CONTEXT.md "Gate Runner" and this ticket's acceptance criteria.
	ContinueOnFailure bool
}

// Runner executes a repository's configured Quality Gates in order.
type Runner struct {
	// Command is the injected CommandRunner every gate's command is
	// executed through.
	Command CommandRunner

	// Now is a seam for deterministic tests; NewRunner sets it to
	// time.Now.
	Now func() time.Time
}

// NewRunner returns a Runner backed by cr, with Now defaulted to
// time.Now.
func NewRunner(cr CommandRunner) *Runner {
	return &Runner{Command: cr, Now: time.Now}
}

// Run executes gates in order inside workDir, stopping after the first
// failure unless opts.ContinueOnFailure is set. It always returns every
// Result produced before stopping (nil if gates is empty).
func (r *Runner) Run(ctx context.Context, workDir string, gates []config.QualityGate, opts Options) []Result {
	var results []Result
	for _, g := range gates {
		res := r.runOne(ctx, workDir, g, opts)
		results = append(results, res)
		if !res.Passed && !opts.ContinueOnFailure {
			break
		}
	}
	return results
}

// runOne executes a single Quality Gate and captures its Result.
// CommandRunner errors (the command could not even be started) are folded
// into a failing Result with ExitCode -1 rather than surfaced as a Go
// error, so a misconfigured gate command is diagnosed the same way a
// legitimate gate failure is: via Feedback, not a crashed orchestration
// run.
func (r *Runner) runOne(ctx context.Context, workDir string, g config.QualityGate, opts Options) Result {
	stdout := newBoundedWriter(opts.MaxOutputBytes)
	stderr := newBoundedWriter(opts.MaxOutputBytes)

	started := r.Now()
	exitCode, err := r.Command.Run(ctx, workDir, g.Command, stdout, stderr)
	finished := r.Now()

	if err != nil {
		exitCode = -1
		_, _ = stderr.Write([]byte("\ngate runner: " + err.Error()))
	}

	return Result{
		Name:       g.Name,
		Command:    g.Command,
		StartedAt:  started,
		FinishedAt: finished,
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		Passed:     exitCode == 0,
	}
}
