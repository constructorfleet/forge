// Package codex implements an Agent Adapter (see CONTEXT.md "Agent
// Adapter") that invokes the OpenAI Codex CLI as a subprocess. It shares its
// prompt-building, result-parsing, and subprocess mechanics with every other
// CLI Agent Adapter via internal/agent/clicommon; this package only supplies
// codex-specific configuration (executable name, invocation flags, and
// environment allowlist).
package codex

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
)

var _ agent.Agent = (*Adapter)(nil)

// Runner executes one Codex CLI invocation. See clicommon.Runner for the
// full contract; aliased here so callers configuring an Adapter don't need
// to import clicommon directly.
type Runner = clicommon.Runner

// defaultExecutable is the Codex CLI binary name used when Adapter.Executable
// is empty.
const defaultExecutable = "codex"

// nonInteractiveArgs invokes the Codex CLI in a headless, non-interactive
// mode that reads the prompt from stdin and exits after one turn, mirroring
// internal/agent/claude's claudePrintFlag/permission-mode handling.
var nonInteractiveArgs = []string{"exec", "--full-auto", "--skip-git-repo-check"}

// allowedEnvVars is the fixed base allowlist of environment variables
// passed to the Codex CLI subprocess, matching internal/agent/claude's
// rationale: the subprocess environment is sanitized, not inherited
// wholesale, so secrets in Forge's own process environment never reach the
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

// defaultAuthEnvVars is the standard set of Codex/OpenAI auth variables
// forwarded by default, on top of allowedEnvVars.
var defaultAuthEnvVars = []string{
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
}

// Adapter is a production Agent Adapter that invokes the Codex CLI as a
// subprocess in the Worker's Workspace.
type Adapter struct {
	// Runner performs the actual subprocess invocation. If nil, Execute
	// uses clicommon.DefaultRunner(Executable), which execs Executable (or
	// "codex" if Executable is empty) for real.
	Runner Runner

	// Executable is the path or name of the Codex CLI binary. Defaults to
	// "codex" if empty.
	Executable string

	// ExtraEnvPassthrough lists additional environment variable NAMES to
	// forward to the subprocess beyond the base allowlist and
	// defaultAuthEnvVars.
	ExtraEnvPassthrough []string
}

// Execute implements agent.Agent by delegating to clicommon.ExecuteCLI.
func (a *Adapter) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	executable := a.Executable
	if executable == "" {
		executable = defaultExecutable
	}
	cfg := clicommon.CLIConfig{
		BackendName:         "codex",
		Runner:              a.Runner,
		Executable:          executable,
		Args:                nonInteractiveArgs,
		AllowedEnvVars:      allowedEnvVars,
		AuthEnvVars:         defaultAuthEnvVars,
		ExtraEnvPassthrough: a.ExtraEnvPassthrough,
	}
	return clicommon.ExecuteCLI(ctx, cfg, req)
}
