// Package claude implements an Agent Adapter (see CONTEXT.md "Agent
// Adapter") that invokes the Claude Code CLI as a subprocess. It translates
// an internal/agent.AgentRequest into a prompt and CLI invocation, and
// parses the CLI's output back into a structured internal/agent.AgentResult.
package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/textcap"
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

// maxCapturedOutputLen bounds how much of stdout/stderr defaultRunner ever
// holds in memory at once, independent of maxDiagnosticLen's
// presentation-time truncation: a small multiple keeps some headroom for
// pre-truncation formatting while still guaranteeing a runaway subprocess
// can't force unbounded memory use.
const maxCapturedOutputLen = 4 * maxDiagnosticLen

// claudePrintFlag invokes Claude Code in headless/print mode: it reads the
// prompt from stdin, prints its final response to stdout, and exits
// without an interactive session.
const claudePrintFlag = "-p"

// streamingArgs requests Claude Code's per-turn streaming JSON output
// (ticket 28): one JSON object per line — assistant messages, tool calls,
// tool results, and a terminal "result" line — instead of only the final
// response text. Execute reconstructs the same final text `-p` alone would
// have printed from this stream (see parseStreamTranscript) and, when
// req.Transcript is set, emits a TranscriptEvent for each recognized line
// along the way.
var streamingArgs = []string{"--output-format", "stream-json", "--verbose"}

// allowedEnvVars is the fixed base allowlist of environment variables
// passed to the Claude Code subprocess. The Agent's environment is
// sanitized rather than inherited wholesale so secrets present in Forge's
// own process environment (API tokens, tracker credentials, etc.) never
// reach the Agent.
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

// defaultAuthEnvVars is the standard set of Claude auth variables forwarded
// by default, on top of allowedEnvVars, so the common headless (API-key)
// case works out of the box without any Adapter configuration. Anything
// beyond this — Bedrock/Vertex/AWS/GOOGLE credentials, for example —
// requires explicit opt-in via Adapter.ExtraEnvPassthrough.
var defaultAuthEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
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

	// ExtraEnvPassthrough lists additional environment variable NAMES to
	// forward to the subprocess beyond the base allowlist and
	// defaultAuthEnvVars. Use this to opt in to cloud-specific credentials
	// Forge does not forward by default — e.g. AWS_ACCESS_KEY_ID,
	// AWS_SECRET_ACCESS_KEY, CLAUDE_CODE_USE_BEDROCK,
	// GOOGLE_APPLICATION_CREDENTIALS, CLAUDE_CODE_USE_VERTEX. Anything not
	// in the base allowlist, defaultAuthEnvVars, or ExtraEnvPassthrough is
	// excluded, regardless of what's set in Forge's own environment.
	ExtraEnvPassthrough []string

	// Now timestamps TranscriptEvents as Execute parses them out of the
	// streamed output (ticket 28). Defaults to time.Now when nil; tests
	// inject a fixed clock for deterministic assertions.
	Now func() time.Time
}

// Execute implements agent.Agent. It builds a prompt from req, invokes
// Claude Code in req.WorkspacePath, and parses the result. Ordinary
// failures — subprocess errors, non-zero exit codes without a valid
// structured result — are surfaced through AgentResult.Status (FAILED)
// rather than a returned error, so callers get uniform handling. Context
// cancellation is the one exception: it is surfaced as a wrapped ctx.Err()
// Go error (in addition to Status FAILED) so a retry loop (tickets 21/24)
// can distinguish "the caller gave up on this attempt" from "the agent
// genuinely failed" and avoid miscounting it against the retry budget.
func (a *Adapter) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	prompt := buildPrompt(req)
	env := sanitizedEnv(a.ExtraEnvPassthrough)

	runner := a.Runner
	if runner == nil {
		runner = a.defaultRunner()
	}

	args := append([]string{claudePrintFlag}, streamingArgs...)
	stdout, stderr, exitCode, err := runner(ctx, req.WorkspacePath, args, prompt, env)

	// finalText is the reconstructed equivalent of what `-p` alone would
	// have printed (see extractFinalText); every downstream use of the raw
	// stdout capture below is replaced with it so transcript capture is
	// transparent to existing diagnostics and result parsing.
	finalText := a.extractFinalText(stdout, req.Transcript)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(fmt.Sprintf("claude adapter: cancelled: %v", ctxErr), finalText, stderr),
		}, fmt.Errorf("claude adapter: cancelled: %w", ctxErr)
	}

	if err != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(fmt.Sprintf("claude adapter: subprocess error: %v", err), finalText, stderr),
		}, nil
	}

	res, ok := parseStructuredResult(finalText)
	if !ok {
		return agent.AgentResult{
			Status: agent.StatusFailed,
			Summary: diagnosticSummary(
				fmt.Sprintf("claude adapter: no structured result found in output (exit code %d)", exitCode),
				finalText, stderr,
			),
		}, nil
	}

	switch agent.AgentStatus(res.Status) {
	case agent.StatusImplemented:
		return agent.AgentResult{Status: agent.StatusImplemented, Summary: res.Summary, Usage: toTokenUsage(res.Usage)}, nil
	case agent.StatusFailed:
		return agent.AgentResult{Status: agent.StatusFailed, Summary: res.Summary, Usage: toTokenUsage(res.Usage)}, nil
	case agent.StatusNeedsInfo:
		if res.NeedsInfo == nil || strings.TrimSpace(res.NeedsInfo.Question) == "" {
			return agent.AgentResult{
				Status: agent.StatusFailed,
				Summary: diagnosticSummary(
					"claude adapter: NEEDS_INFO result missing a needs_info question",
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
			Usage: toTokenUsage(res.Usage),
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

// extractFinalText recovers the final response text from stdout — the
// same text `-p` alone would have printed — by parsing it as
// `--output-format stream-json --verbose` output, and, when sink is
// non-nil, emitting a TranscriptEvent for every message/tool call/tool
// result it recognizes along the way (ticket 28).
//
// Parsing is entirely best-effort: any failure to recognize the stream —
// including a panic from a malformed line this code doesn't anticipate —
// falls back to treating stdout itself as the final text, exactly matching
// this Adapter's pre-transcript behavior, so a streaming-parse bug can
// never change an Issue's outcome (ticket 28's degrade-gracefully
// requirement).
func (a *Adapter) extractFinalText(stdout string, sink agent.TranscriptSink) (finalText string) {
	finalText = stdout
	defer func() {
		if r := recover(); r != nil {
			finalText = stdout
		}
	}()

	now := a.Now
	if now == nil {
		now = time.Now
	}
	text, ok := parseStreamTranscript(stdout, sink, now)
	if !ok {
		return stdout
	}
	return text
}

func toTokenUsage(in *usageFields) *agent.TokenUsage {
	if in == nil {
		return nil
	}
	return &agent.TokenUsage{
		InputTokens:  in.InputTokens,
		OutputTokens: in.OutputTokens,
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
// from allowedEnvVars, defaultAuthEnvVars, and extra (an Adapter's
// opt-in ExtraEnvPassthrough) only, looking each up in Forge's own process
// environment. Anything not named in one of those three sets — including
// secrets such as tracker or CI tokens — never reaches the Agent.
func sanitizedEnv(extra []string) []string {
	capacity := len(allowedEnvVars) + len(defaultAuthEnvVars) + len(extra)
	seen := make(map[string]bool, capacity)
	keys := make([]string, 0, capacity)
	for _, group := range [][]string{allowedEnvVars, defaultAuthEnvVars, extra} {
		for _, key := range group {
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}

	env := make([]string, 0, len(keys))
	for _, key := range keys {
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

		// Bounded, tail-preserving writers (internal/textcap, shared with
		// internal/gate): an unbounded bytes.Buffer here would let a
		// runaway subprocess force Forge to hold arbitrarily large output
		// in memory (see maxCapturedOutputLen).
		stdout := textcap.NewTailWriter(maxCapturedOutputLen)
		stderr := textcap.NewTailWriter(maxCapturedOutputLen)
		cmd.Stdout = stdout
		cmd.Stderr = stderr

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
