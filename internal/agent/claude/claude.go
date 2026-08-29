// Package claude implements an Agent Adapter (see CONTEXT.md "Agent
// Adapter") that invokes the Claude Code CLI as a subprocess. It translates
// an internal/agent.AgentRequest into a prompt and CLI invocation, and
// parses the CLI's output back into a structured internal/agent.AgentResult.
package claude

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
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
//
// onLine, when non-nil, is invoked with each stdout line as it is produced
// (issue 36) — the seam that lets Execute parse and persist the transcript
// incrementally, so a killed/timed-out run keeps its events up to the
// moment of the kill rather than losing them to end-of-run batch capture.
// The returned stdout is still the full (bounded) capture for diagnostics
// and the non-stream fallback. Implementations may call onLine from the
// same goroutine that runs the subprocess; Execute's handler is safe for
// that and never panics back out.
type Runner func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(line string)) (stdout, stderr string, exitCode int, err error)

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

// defaultPermissionMode is the `--permission-mode` value used when an
// Adapter's PermissionMode is unset (ticket 30, "Agent runs need a
// non-interactive permission mode"). Execute runs Claude Code as an
// unattended subprocess — nothing can answer an interactive tool-use
// permission prompt — so the default must not be a mode that can ever
// block on one. bypassPermissions auto-approves every tool call, relying
// on the Issue's Workspace (an isolated Git worktree) as the safety
// boundary instead.
const defaultPermissionMode = "bypassPermissions"

// streamingArgs requests Claude Code's per-turn streaming JSON output
// (ticket 28): one JSON object per line — assistant messages, tool calls,
// tool results, and a terminal "result" line — instead of only the final
// response text. Execute reconstructs the same final text `-p` alone would
// have printed from this stream (see parseStreamTranscript) and, when
// req.Transcript is set, emits a TranscriptEvent for each recognized line
// along the way.
var streamingArgs = []string{"--output-format", "stream-json", "--verbose"}

// jsonSchemaArgs requests CLI-side enforcement of the result envelope shape
// (issue 20/ticket 32, supported as of Claude Code 2.1.222): the CLI
// validates the model's final answer against resultJSONSchema before ever
// printing it, so Execute can decode the terminal result text directly
// (parseSchemaResult) instead of parsing a fenced ```json block out of
// free-form output after the fact. This composes with streamingArgs: with
// `--output-format stream-json`, the schema-conforming text is delivered as
// the `result` field of the terminal "result" line (see streamLine).
var jsonSchemaArgs = []string{"--json-schema", resultJSONSchema}

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

	// PermissionMode sets Claude Code's `--permission-mode` flag, so tool
	// calls made during an unattended Execute run don't stall on an
	// interactive permission prompt nothing can answer. Defaults to
	// defaultPermissionMode ("bypassPermissions") when empty.
	PermissionMode string

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

	// Timeout bounds one Execute invocation so a wedged subprocess cannot
	// block a Worker forever (issue 33, "Agent runs need a timeout"). It is
	// a liveness (idle) timeout, not a flat wall-clock cap: each stdout
	// line the subprocess produces resets the deadline (see
	// clicommon.IdleTimeout), so a long-but-progressing run is never
	// killed — only a genuine stall (no output at all for Timeout) trips
	// it. Zero disables the timeout, matching pre-ticket-33 behavior;
	// production wiring always sets this from config.Config.Agent.Timeout,
	// which defaults to a nonzero value.
	Timeout time.Duration
}

// Execute implements agent.Agent. It builds a prompt from req, invokes
// Claude Code in req.WorkspacePath, and parses the result. Ordinary
// failures — subprocess errors, non-zero exit codes without a valid
// structured result, and a stalled subprocess hitting a.Timeout (issue 33)
// — are surfaced through AgentResult.Status (FAILED) rather than a returned
// error, so callers get uniform handling and a timeout is retried like any
// other agent failure. Context cancellation is the one exception: it is
// surfaced as a wrapped ctx.Err() Go error (in addition to Status FAILED)
// so a retry loop (tickets 21/24) can distinguish "the caller gave up on
// this attempt" from "the agent genuinely failed" and avoid miscounting it
// against the retry budget.
func (a *Adapter) Execute(ctx context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	prompt := buildPrompt(req)
	env := sanitizedEnv(a.ExtraEnvPassthrough)

	runner := a.Runner
	if runner == nil {
		runner = a.defaultRunner()
	}

	permissionMode := a.PermissionMode
	if permissionMode == "" {
		permissionMode = defaultPermissionMode
	}
	args := []string{claudePrintFlag, "--permission-mode", permissionMode}
	args = append(args, streamingArgs...)
	args = append(args, jsonSchemaArgs...)

	// Parse the stream-json output incrementally, as each line arrives, so
	// transcript events (and their real per-event timestamps) are captured
	// and persisted from the very first turn and up to the moment a
	// killed/timed-out run is cut off (issue 36) — not reconstructed after
	// the fact from a tail-truncated buffer that has already lost the run's
	// opening events.
	now := a.Now
	if now == nil {
		now = time.Now
	}
	// runCtx is derived from ctx with an idle-timeout watchdog (issue 33):
	// touch resets the deadline on every stdout line the subprocess
	// produces, so only a genuine stall (no output for a.Timeout) cancels
	// runCtx, distinct from ctx itself being canceled by its parent. stop
	// must run before Execute returns to release the watchdog goroutine.
	runCtx, timedOut, touch, stop := clicommon.IdleTimeout(ctx, a.Timeout)
	defer stop()

	parser := newStreamParser(req.Transcript, now)
	onLine := func(line string) {
		touch()
		parser.consume(line)
	}
	stdout, stderr, exitCode, err := runner(runCtx, req.WorkspacePath, args, prompt, env, onLine)

	// finalText is the reconstructed equivalent of what `-p` alone would
	// have printed; every downstream use of the raw stdout capture below is
	// replaced with it so transcript capture is transparent to existing
	// diagnostics and result parsing. A recognized stream yields the
	// streamed result; a non-stream or capture-aborted run degrades to raw
	// stdout, exactly matching this Adapter's pre-transcript behavior.
	if !parser.parsedAny && !parser.aborted {
		// The Runner delivered nothing through onLine (e.g. a test double
		// that returns canned stdout without streaming). Fall back to
		// parsing the captured buffer so those callers still get capture.
		for _, line := range strings.Split(stdout, "\n") {
			parser.consume(line)
		}
	}
	finalText := parser.reconstructedFinalText(stdout)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(fmt.Sprintf("claude adapter: cancelled: %v", ctxErr), finalText, stderr),
		}, fmt.Errorf("claude adapter: cancelled: %w", ctxErr)
	}

	// A timeout is reported as an ordinary FAILED outcome (err == nil), not
	// the wrapped ctx.Err() Go error ctx-cancellation above returns: unlike
	// an operator-driven cancellation (which should abort the run without
	// counting against the retry budget), a wedged agent is exactly the
	// kind of failure the retry budget/`forge retry` exists to recover
	// from, so it must flow through the normal StatusFailed handling.
	if timedOut() {
		return agent.AgentResult{
			Status: agent.StatusFailed,
			Summary: diagnosticSummary(
				fmt.Sprintf("claude adapter: agent timed out after %s with no output", a.Timeout),
				finalText, stderr,
			),
		}, nil
	}

	if err != nil {
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(fmt.Sprintf("claude adapter: subprocess error: %v", err), finalText, stderr),
		}, nil
	}

	// A CLI-level error result (a non-conforming request, permission-request
	// the unattended run couldn't satisfy, etc.) is diagnosed distinctly
	// from "the output didn't decode", since in that case there was never a
	// result to decode in the first place.
	if isErr, subtype := parser.resultError(); isErr {
		msg := fmt.Sprintf("claude adapter: CLI reported an error result (exit code %d)", exitCode)
		if subtype != "" {
			msg = fmt.Sprintf("claude adapter: CLI reported an error result (subtype %q, exit code %d)", subtype, exitCode)
		}
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: diagnosticSummary(msg, finalText, stderr),
		}, nil
	}

	// parseSchemaResult decodes the `--json-schema`-conforming result
	// directly; parseStructuredResult (ticket 27's tolerant fenced-block
	// scanner) is only a fallback, for output that didn't go through schema
	// enforcement.
	res, ok := parseSchemaResult(finalText)
	if !ok {
		res, ok = parseStructuredResult(finalText)
	}
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
	return func(ctx context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		clicommon.ConfigureProcessGroup(cmd)

		// Bounded, tail-preserving writers (internal/textcap, shared with
		// internal/gate): an unbounded bytes.Buffer here would let a
		// runaway subprocess force Forge to hold arbitrarily large output
		// in memory (see maxCapturedOutputLen). stdout is still captured in
		// full (bounded) for diagnostics and the non-stream fallback; onLine
		// additionally sees each line live as it is produced (issue 36).
		stdoutTail := textcap.NewTailWriter(maxCapturedOutputLen)
		stderr := textcap.NewTailWriter(maxCapturedOutputLen)
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
		// to hold an arbitrarily long single line (a large tool result on
		// one JSON line), unlike bufio.Scanner's fixed token cap.
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
