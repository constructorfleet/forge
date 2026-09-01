// Package execution defines the ExecutionBackend seam: a neutral surface
// for preparing a long-lived environment (Workspace, subprocess runner,
// coding Agent) for one Issue execution, independent of whether that
// environment runs in-process on the local host or on a remote backend.
// This package knows nothing about any particular backend; the LocalHost
// backend (internal/execution/localhost) is the first, in-process
// implementation.
package execution

import (
	"context"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
)

// WorkspaceRequest describes the Workspace an ExecutionEnvironment must
// prepare: which Execution and Issue it belongs to, and which revision to
// build it from. base carries the same meaning as workspace.Manager.Create's
// base parameter: the revision captured for this Issue at its READY
// transition (a plain git base, a single dependency's branch for a stacked
// build, or a pre-built integration branch for multiple dependencies).
type WorkspaceRequest struct {
	ExecutionID string
	IssueID     string
	Base        string
}

// Command is one subprocess invocation to run inside an
// ExecutionEnvironment's Workspace.
type Command struct {
	// Name labels the command for reporting (e.g. a gate's name).
	Name string

	// Command is the shell command line to run, via a shell (e.g. `sh -c`).
	// Ignored when Args is non-empty.
	Command string

	// Args, when non-empty, is the full argv (executable plus its
	// arguments) to run directly, with no shell interpretation. Prefer
	// this over Command whenever an argument's content is not
	// shell-safe to embed literally (e.g. an Agent invocation's prompt or
	// a JSON schema).
	Args []string

	// WorkDir is the directory the command runs in, relative to the
	// Workspace root. Empty means the Workspace root itself.
	WorkDir string

	// Stdin, when non-empty, is written to the command's standard input
	// before it runs (e.g. an Agent invocation's prompt). Empty means no
	// standard input.
	Stdin string

	// Env, when non-nil, replaces the command's environment with exactly
	// these "KEY=VALUE" entries (e.g. an Agent invocation's sanitized auth
	// variables). Nil means the command runs with the executor's own
	// default environment.
	Env []string
}

// Result is the outcome of running a Command.
type Result struct {
	Name       string
	Command    string
	StartedAt  time.Time
	FinishedAt time.Time
	ExitCode   int
	Stdout     string
	Stderr     string
}

// ExecutionBackend prepares long-lived ExecutionEnvironments. One Prepare
// call corresponds to one Issue execution.
type ExecutionBackend interface {
	Prepare(ctx context.Context, req WorkspaceRequest) (ExecutionEnvironment, error)
}

// ExecutionEnvironment is a long-lived environment for one Issue execution:
// a prepared Workspace, a way to run subprocesses inside it, and the coding
// Agent that operates on it. Callers create one per Issue execution and
// Cleanup it when the execution ends.
type ExecutionEnvironment interface {
	// Workspace returns the Workspace this environment prepared.
	Workspace() domain.Workspace

	// Execute runs cmd inside the Workspace and returns its outcome. Each
	// call is a one-shot, coarse-grained subprocess run (e.g. one quality
	// gate); it is not a persistent shell session.
	Execute(ctx context.Context, cmd Command) (Result, error)

	// Agent returns the coding Agent that operates on this environment's
	// Workspace.
	Agent() agent.Agent

	// Cleanup releases the environment's Workspace and any other
	// resources it holds. Callers must not use the environment afterward.
	Cleanup(ctx context.Context) error
}
