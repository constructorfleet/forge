package clicommon

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
)

// StreamParser incrementally turns one CLI backend's structured output
// stream into transcript events and reconstructs the assistant text the
// result envelope is parsed from. Adapters whose CLI can emit a structured
// per-line event stream (codex `exec --json`, opencode `--format json`, pi
// `--mode json`) supply one via CLIConfig.NewStreamParser so ExecuteCLI
// persists a transcript as it occurs and at per-turn granularity, matching
// internal/agent/claude rather than emitting one coarse blob after the run.
//
// A StreamParser is stateful: one instance serves exactly one ExecuteCLI
// call. Line is invoked once per stdout line as it arrives; Result is read
// once, after the stream ends.
type StreamParser interface {
	// Line consumes one line of the backend's stdout (newline already
	// trimmed) and returns the transcript events it produced, if any. A
	// line the parser does not recognize (blank lines, non-JSON banner
	// text) yields no events.
	Line(line string) []agent.TranscriptEvent
	// Result returns the assistant text ExecuteCLI scans for the result
	// envelope (ParseStructuredResult) once every line has been consumed —
	// i.e. the model's reconstructed final message, not the raw event
	// stream.
	Result() string
}

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

	// NewStreamParser, when non-nil, builds a StreamParser for this
	// invocation: ExecuteCLI feeds it each stdout line so the transcript is
	// emitted incrementally and the result envelope is read from the
	// parser's reconstructed final text instead of the raw stream. When
	// nil, ExecuteCLI keeps the coarse fallback (one terminal event over
	// the full stdout) for a CLI without a structured stream.
	NewStreamParser func() StreamParser

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
// Transcript persistence has two modes. When cfg.NewStreamParser is set,
// ExecuteCLI emits a TranscriptEvent per parsed line as the run proceeds, so
// a killed/timed-out/failed run keeps every event up to the moment it died
// (issue #257) at per-turn granularity like internal/agent/claude. When it
// is nil, ExecuteCLI falls back to emitting a single coarse
// TranscriptEventMessage carrying the backend's full output. Either way, and
// on every exit path — success, subprocess error, or cancellation — a run
// that captured any output persists at least one event: no attempt is ever a
// blank transcript.
func ExecuteCLI(ctx context.Context, cfg CLIConfig, req agent.AgentRequest) (agent.AgentResult, error) {
	prompt := BuildPrompt(cfg.BackendName, req)
	env := SanitizedEnv(cfg.AllowedEnvVars, cfg.AuthEnvVars, cfg.ExtraEnvPassthrough)

	runner := cfg.Runner
	if runner == nil {
		runner = DefaultRunner(cfg.Executable)
	}

	var parser StreamParser
	if cfg.NewStreamParser != nil {
		parser = cfg.NewStreamParser()
	}

	// streamed counts events emitted live through onLine so the exit paths
	// can tell whether the transcript is already non-blank before falling
	// back to a coarse diagnostic emit.
	streamed := 0
	var onLine func(string)
	if parser != nil {
		onLine = func(line string) {
			events := parser.Line(line)
			if req.Transcript == nil {
				return
			}
			for _, ev := range events {
				req.Transcript.Emit(ev)
				streamed++
			}
		}
	}

	stdout, stderr, exitCode, err := runner(ctx, req.WorkspacePath, cfg.Args, prompt, env, onLine)

	// resultText is what the result envelope is parsed from: the parser's
	// reconstructed final message when streaming, else the raw stdout.
	resultText := stdout
	if parser != nil {
		resultText = parser.Result()
	}

	// emitFallback guarantees a captured-but-unstreamed run is not a blank
	// transcript. It is a no-op when nothing was captured, when events
	// already streamed, or when there is no sink — so the streaming happy
	// path never double-emits.
	emitFallback := func() {
		if req.Transcript == nil || streamed > 0 {
			return
		}
		text := DiagnosticSummary("", stdout, stderr)
		if text == "" {
			return
		}
		req.Transcript.Emit(agent.TranscriptEvent{
			Type: agent.TranscriptEventMessage,
			Role: "assistant",
			Text: Truncate(text, MaxDiagnosticLen),
		})
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		emitFallback()
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: DiagnosticSummary(fmt.Sprintf("%s adapter: cancelled: %v", cfg.BackendName, ctxErr), stdout, stderr),
		}, fmt.Errorf("%s adapter: cancelled: %w", cfg.BackendName, ctxErr)
	}

	if err != nil {
		emitFallback()
		return agent.AgentResult{
			Status:  agent.StatusFailed,
			Summary: DiagnosticSummary(fmt.Sprintf("%s adapter: subprocess error: %v", cfg.BackendName, err), stdout, stderr),
		}, nil
	}

	structured, ok := ParseStructuredResult(resultText)
	res := Resolve(cfg.BackendName, structured, ok, exitCode, stdout, stderr)

	// Non-streaming fallback: one coarse assistant message over the full
	// stdout, preserving the pre-#257 behavior for adapters without a
	// StreamParser. The streaming path relies on the per-line emits above,
	// with emitFallback covering a run that produced no recognizable event.
	if parser == nil {
		if req.Transcript != nil && stdout != "" {
			req.Transcript.Emit(agent.TranscriptEvent{
				Type: agent.TranscriptEventMessage,
				Role: "assistant",
				Text: Truncate(stdout, MaxDiagnosticLen),
			})
		}
	} else {
		emitFallback()
	}

	return res, nil
}
