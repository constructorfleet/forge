// Package codex implements an Agent Adapter (see CONTEXT.md "Agent
// Adapter") that invokes the OpenAI Codex CLI as a subprocess. It shares its
// prompt-building, result-parsing, and subprocess mechanics with every other
// CLI Agent Adapter via internal/agent/clicommon; this package only supplies
// codex-specific configuration (executable name, invocation flags, and
// environment allowlist).
package codex

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
)

var _ agent.Agent = (*Adapter)(nil)
var _ agent.SemanticProfiler = (*Adapter)(nil)

// codexSemanticProfile is the v1 baseline Semantic Profile for Codex: it
// exposes no Semantic Capabilities natively, so Forge fills every
// capability through a Model Context Protocol server (see CONTEXT.md
// "Injection Channel").
var codexSemanticProfile = agent.SemanticProfile{
	Capabilities: agent.SemanticCapabilities{},
	Channel:      agent.InjectionChannelMCP,
}

// SemanticProfile declares Codex's native Semantic Capabilities and
// Injection Channel (see CONTEXT.md "Semantic Profile"), implementing
// agent.SemanticProfiler.
func (a *Adapter) SemanticProfile() agent.SemanticProfile {
	return codexSemanticProfile
}

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
	args := append(append([]string{}, nonInteractiveArgs...), mcpArgs(req)...)
	cfg := clicommon.CLIConfig{
		BackendName:         "codex",
		Runner:              a.Runner,
		Executable:          executable,
		Args:                args,
		AllowedEnvVars:      allowedEnvVars,
		AuthEnvVars:         defaultAuthEnvVars,
		ExtraEnvPassthrough: a.ExtraEnvPassthrough,
	}
	return clicommon.ExecuteCLI(ctx, cfg, req)
}

// forgeExecutable resolves the currently-running forge binary's absolute
// path (os.Executable) so Codex spawns the exact same forge that dispatched
// it, falling back to the bare "forge" name (resolved via PATH by Codex's
// own subprocess spawn) if that lookup fails. A package var so tests can
// override it.
var forgeExecutable = func() string {
	path, err := os.Executable()
	if err != nil {
		return "forge"
	}
	return path
}

// mcpArgs translates req.Semantic.MCPServers (see CONTEXT.md "Injection
// Channel": InjectionChannelMCP) into the Codex CLI flags that point Codex
// at Forge's backend-neutral MCP server (`forge internal-mcp --workspace
// <path>`, issue #127): a `-c mcp_servers.forgelsp.command=[...]` config
// override plus `--ignore-user-config`, so Codex spawns and owns that
// subprocess itself rather than reading it from the user's own Codex
// config. Returns nil when Semantic.MCPServers is empty — the
// SemanticProvider seam (internal/semantic) already degrades to an empty
// list whenever lsp.enabled is false or Codex's declared capabilities need
// no fill, so no special-casing of that config is needed here.
func mcpArgs(req agent.AgentRequest) []string {
	if len(req.Semantic.MCPServers) == 0 {
		return nil
	}

	command := []string{forgeExecutable(), "internal-mcp", "--workspace", req.WorkspacePath}
	quoted := make([]string, len(command))
	for i, c := range command {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	value := fmt.Sprintf("mcp_servers.forgelsp.command=[%s]", strings.Join(quoted, ","))

	return []string{"-c", value, "--ignore-user-config"}
}
