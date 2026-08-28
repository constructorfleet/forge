// Package claude implements an Agent Adapter (see CONTEXT.md "Agent
// Adapter") that invokes the Claude Code CLI as a subprocess. It translates
// an internal/agent.AgentRequest into a prompt and CLI invocation, and
// parses the CLI's output back into a structured internal/agent.AgentResult.
package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
)

var _ agent.Agent = (*Adapter)(nil)

// Runner executes one Claude Code invocation: args are passed on the
// command line, stdin carries the prompt, dir is the working directory
// (the Issue's Workspace), and env is the fully-resolved (already
// sanitized) environment for the subprocess. Implementations must honor
// ctx cancellation by killing the subprocess. Tests inject a fake Runner so
// they never shell out to a real claude binary; production code uses
// defaultRunner.
type Runner func(ctx context.Context, dir string, args []string, stdin string, env []string) (stdout, stderr string, exitCode int, err error)

// maxDiagnosticLen bounds how much of stdout/stderr is folded into an
// AgentResult.Summary for FAILED outcomes, keeping diagnostics readable and
// bounded (see CONTEXT.md "Gate Runner" for the same bounded-feedback
// principle applied here to Agent diagnostics).
const maxDiagnosticLen = 4000

// allowedEnvVars is the fixed allowlist of environment variables passed to
// the Claude Code subprocess. The Agent's environment is sanitized rather
// than inherited wholesale so secrets present in Forge's own process
// environment (API tokens, tracker credentials, etc.) never reach the
// Agent.
var allowedEnvVars = []string{
	"PATH",
	"HOME",
	"USER",
	"LANG",
	"LC_ALL",
	"TERM",
	"TMPDIR",
	"SHELL",
}

// Adapter is a production Agent Adapter that invokes Claude Code as a
// subprocess in the Worker's Workspace.
type Adapter struct {
	// Runner performs the actual subprocess invocation. If nil, Execute
	// uses defaultRunner, which execs Executable (or "claude" if
	// Executable is empty) for real.
	Runner Runner

	// Executable is the path or name of the Claude Code CLI binary used by
	// defaultRunner. Defaults to "claude" if empty.
	Executable string
}

// Execute implements agent.Agent. It builds a prompt from req, invokes
// Claude Code in req.WorkspacePath, and parses the result. Outcomes —
// including subprocess errors, cancellation, and non-zero exit codes
// without a valid structured result — are surfaced through
// AgentResult.Status rather than a returned error, so callers get uniform
// handling regardless of failure mode.
func (a *Adapter) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	prompt := buildPrompt(req)
	env := sanitizedEnv()

	runner := a.Runner
	if runner == nil {
		runner = a.defaultRunner()
	}

	stdout, stderr, exitCode, err := runner(ctx, req.WorkspacePath, []string{"-p"}, prompt, env)
	if err != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(fmt.Sprintf("claude adapter: subprocess error: %v", err), stdout, stderr),
		}, nil
	}

	res, ok := parseStructuredResult(stdout)
	if !ok {
		return agent.AgentResult{
			Status: agent.StatusFailed,
			Summary: diagnosticSummary(
				fmt.Sprintf("claude adapter: no structured result found in output (exit code %d)", exitCode),
				stdout, stderr,
			),
		}, nil
	}

	switch agent.AgentStatus(res.Status) {
	case agent.StatusImplemented:
		return agent.AgentResult{Status: agent.StatusImplemented, Summary: res.Summary}, nil
	case agent.StatusFailed:
		return agent.AgentResult{Status: agent.StatusFailed, Summary: res.Summary}, nil
	case agent.StatusNeedsInfo:
		if res.NeedsInfo == nil {
			return agent.AgentResult{
				Status: agent.StatusFailed,
				Summary: diagnosticSummary(
					"claude adapter: NEEDS_INFO result missing needs_info details",
					stdout, stderr,
				),
			}, nil
		}
		return agent.AgentResult{
			Status:  agent.StatusNeedsInfo,
			Summary: res.Summary,
			NeedsInfo: &agent.NeedsInfoDetail{
				Question: res.NeedsInfo.Question,
				Context:  res.NeedsInfo.Context,
			},
		}, nil
	default:
		// parseStructuredResult only returns ok=true for recognized
		// statuses, so this is unreachable in practice; handled for
		// exhaustiveness.
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(fmt.Sprintf("claude adapter: unrecognized status %q", res.Status), stdout, stderr),
		}, nil
	}
}

// diagnosticSummary composes a human-readable Summary for FAILED outcomes,
// folding in bounded captures of stdout/stderr so a human (or a repair
// prompt on retry, via Feedback) has enough to diagnose the failure.
func diagnosticSummary(prefix, stdout, stderr string) string {
	var b strings.Builder
	b.WriteString(prefix)
	if stdout = strings.TrimSpace(stdout); stdout != "" {
		b.WriteString("\n\nstdout:\n")
		b.WriteString(truncate(stdout, maxDiagnosticLen))
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		b.WriteString("\n\nstderr:\n")
		b.WriteString(truncate(stderr, maxDiagnosticLen))
	}
	return b.String()
}

// truncate bounds s to at most n bytes, marking that it was cut so readers
// know the diagnostic is partial.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

// sanitizedEnv builds the environment passed to the Claude Code subprocess
// from allowedEnvVars only, looking each up in Forge's own process
// environment. Anything not on the allowlist — including secrets such as
// tracker or CI tokens — never reaches the Agent.
func sanitizedEnv() []string {
	env := make([]string, 0, len(allowedEnvVars))
	for _, key := range allowedEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}

// defaultRunner returns the Runner used in production: it execs the real
// Claude Code CLI (a.Executable, defaulting to "claude") via
// exec.CommandContext, so ctx cancellation kills the subprocess.
func (a *Adapter) defaultRunner() Runner {
	executable := a.Executable
	if executable == "" {
		executable = "claude"
	}
	return func(ctx context.Context, dir string, args []string, stdin string, env []string) (string, string, int, error) {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
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
		return stdout.String(), stderr.String(), exitCode, err
	}
}
