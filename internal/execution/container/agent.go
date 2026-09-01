package container

import (
	"context"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/execution"
)

// AgentFactory builds the coding Agent one environment exposes, from that
// environment itself. A factory that wants the Agent to run inside the
// container builds an Adapter (e.g. internal/agent/claude.Adapter) whose
// Runner is NewAgentRunner(env, executable), so its subprocess invocations
// go through env.Execute (in-container) instead of the host.
type AgentFactory func(env execution.ExecutionEnvironment) agent.Agent

// AgentRunnerFunc matches the Runner signature every CLI Agent Adapter
// accepts (internal/agent/claude.Runner, internal/agent/clicommon.Runner):
// args are the CLI's arguments, stdin carries the prompt, dir is the
// working directory the Adapter asks for, and env is the sanitized
// subprocess environment. Implementations must honor ctx cancellation.
type AgentRunnerFunc func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(line string)) (stdout, stderr string, exitCode int, err error)

// NewAgentRunner returns an AgentRunnerFunc that runs executable (with the
// args a CLI Agent Adapter passes) inside env's container through Execute,
// forwarding stdin and env unchanged. It ignores dir: an Agent Adapter
// always asks to run at the Issue's Workspace root, which the container
// backend mounts at WorkspaceMountPath and every Execute call already runs
// relative to, so no translation from a host path is needed.
//
// onLine, when non-nil, receives each line of the command's captured
// stdout after the command finishes (Execute is one-shot, not a streaming
// call), so a caller relying on live per-line delivery sees it delivered
// in one batch rather than incrementally.
func NewAgentRunner(env execution.ExecutionEnvironment, executable string) AgentRunnerFunc {
	return func(ctx context.Context, _ string, args []string, stdin string, envVars []string, onLine func(string)) (string, string, int, error) {
		argv := make([]string, 0, len(args)+1)
		argv = append(argv, executable)
		argv = append(argv, args...)

		result, err := env.Execute(ctx, execution.Command{
			Name:  "agent",
			Args:  argv,
			Stdin: stdin,
			Env:   envVars,
		})
		if err != nil {
			return result.Stdout, result.Stderr, result.ExitCode, err
		}

		if onLine != nil {
			for _, line := range strings.Split(result.Stdout, "\n") {
				if line == "" {
					continue
				}
				onLine(line)
			}
		}
		return result.Stdout, result.Stderr, result.ExitCode, nil
	}
}
