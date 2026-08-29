package clicommon

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
)

// CLIConfig configures one CLI-backed Agent Adapter invocation via
// ExecuteCLI. BackendName identifies the adapter for diagnostics (e.g.
// "codex", "opencode", "pi"). Runner defaults to DefaultRunner(Executable)
// when nil, letting tests inject a fake Runner so they never shell out to a
// real binary.
type CLIConfig struct {
	BackendName string
	Runner      Runner
	Executable  string
	// Args are the CLI arguments passed on every invocation (e.g. flags
	// selecting non-interactive/headless mode).
	Args []string

	// AllowedEnvVars, AuthEnvVars, and ExtraEnvPassthrough together form the
	// subprocess environment allowlist (see SanitizedEnv).
	AllowedEnvVars      []string
	AuthEnvVars         []string
	ExtraEnvPassthrough []string
}

// ExecuteCLI builds a prompt from req, invokes the configured CLI backend in
// req.WorkspacePath, and resolves its output into an agent.AgentResult. It
// is the shared implementation behind every CLI Agent Adapter
// (internal/agent/codex, internal/agent/opencode, internal/agent/pi),
// mirroring internal/agent/claude.Adapter.Execute's control flow: ordinary
// failures (subprocess errors, no structured result) surface through
// AgentResult.Status rather than a returned error; context cancellation is
// the one exception, additionally surfaced as a wrapped ctx.Err() so a
// retry loop can distinguish "the caller gave up" from "the agent failed".
//
// Transcript capture here is best-effort and coarser than
// internal/agent/claude's: these CLIs are not assumed to emit a structured
// per-turn stream, so when req.Transcript is set, ExecuteCLI emits exactly
// one TranscriptEventMessage carrying the backend's full output once the
// run completes, rather than per-message/tool-call granularity.
func ExecuteCLI(ctx context.Context, cfg CLIConfig, req agent.AgentRequest) (agent.AgentResult, error) {
	prompt := BuildPrompt(cfg.BackendName, req)
	env := SanitizedEnv(cfg.AllowedEnvVars, cfg.AuthEnvVars, cfg.ExtraEnvPassthrough)

	runner := cfg.Runner
	if runner == nil {
		runner = DefaultRunner(cfg.Executable)
	}

	stdout, stderr, exitCode, err := runner(ctx, req.WorkspacePath, cfg.Args, prompt, env, nil)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: DiagnosticSummary(fmt.Sprintf("%s adapter: cancelled: %v", cfg.BackendName, ctxErr), stdout, stderr),
		}, fmt.Errorf("%s adapter: cancelled: %w", cfg.BackendName, ctxErr)
	}

	if err != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: DiagnosticSummary(fmt.Sprintf("%s adapter: subprocess error: %v", cfg.BackendName, err), stdout, stderr),
		}, nil
	}

	structured, ok := ParseStructuredResult(stdout)
	res := Resolve(cfg.BackendName, structured, ok, exitCode, stdout, stderr)

	if req.Transcript != nil && stdout != "" {
		req.Transcript.Emit(agent.TranscriptEvent{
			Type: agent.TranscriptEventMessage,
			Role: "assistant",
			Text: Truncate(stdout, MaxDiagnosticLen),
		})
	}

	return res, nil
}
