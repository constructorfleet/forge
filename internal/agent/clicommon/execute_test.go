package clicommon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

func fixedRunner(stdout, stderr string, exitCode int, err error) Runner {
	return func(context.Context, string, []string, string, []string, func(string)) (string, string, int, error) {
		return stdout, stderr, exitCode, err
	}
}

func TestExecuteCLI_ImplementedResult(t *testing.T) {
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"done\"}\n```\n", "", 0, nil),
	}
	res, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{})
	if err != nil {
		t.Fatalf("ExecuteCLI: %v", err)
	}
	if res.Status != agent.StatusImplemented || res.Summary != "done" {
		t.Fatalf("res = %+v, want IMPLEMENTED/done", res)
	}
}

func TestExecuteCLI_SubprocessErrorIsFailedWithoutGoError(t *testing.T) {
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("", "boom", 1, errors.New("exec failed")),
	}
	res, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{})
	if err != nil {
		t.Fatalf("ExecuteCLI returned error %v, want nil (ordinary failures surface via Status)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestExecuteCLI_ContextCancellationSurfacesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("", "", 0, nil),
	}
	res, err := ExecuteCLI(ctx, cfg, agent.AgentRequest{})
	if err == nil {
		t.Fatalf("ExecuteCLI: want a wrapped context error, got nil")
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestExecuteCLI_NoStructuredResultIsFailed(t *testing.T) {
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("no json here", "", 0, nil),
	}
	res, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{})
	if err != nil {
		t.Fatalf("ExecuteCLI: %v", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestExecuteCLI_EmitsTranscriptMessageWhenSinkProvided(t *testing.T) {
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("some narration\n```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"done\"}\n```\n", "", 0, nil),
	}
	sink := agent.NewTranscriptRecorder()
	_, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{Transcript: sink})
	if err != nil {
		t.Fatalf("ExecuteCLI: %v", err)
	}
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("Events() = %d, want 1", len(events))
	}
	if events[0].Type != agent.TranscriptEventMessage || events[0].Role != "assistant" {
		t.Fatalf("events[0] = %+v, want an assistant MESSAGE event", events[0])
	}
}

func TestExecuteCLI_PassesPromptOnStdinAndSanitizedEnv(t *testing.T) {
	t.Setenv("FORGE_CLICOMMON_TEST_ALLOWED", "yes")
	var gotStdin string
	var gotEnv []string
	cfg := CLIConfig{
		BackendName:    "codex",
		AllowedEnvVars: []string{"FORGE_CLICOMMON_TEST_ALLOWED"},
		Runner: func(_ context.Context, _ string, _ []string, stdin string, env []string, _ func(string)) (string, string, int, error) {
			gotStdin = stdin
			gotEnv = env
			return "```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"ok\"}\n```\n", "", 0, nil
		},
	}
	_, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{})
	if err != nil {
		t.Fatalf("ExecuteCLI: %v", err)
	}
	if gotStdin == "" {
		t.Fatalf("stdin was empty, want the rendered prompt")
	}
	found := false
	for _, e := range gotEnv {
		if e == "FORGE_CLICOMMON_TEST_ALLOWED=yes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env = %v, want FORGE_CLICOMMON_TEST_ALLOWED=yes", gotEnv)
	}
}

// lineRunner drives onLine with each of lines in order (simulating a CLI
// that streams stdout line by line), then returns the joined lines as the
// full captured stdout — mirroring DefaultRunner's contract.
func lineRunner(lines []string, stderr string, exitCode int, err error) Runner {
	return func(_ context.Context, _ string, _ []string, _ string, _ []string, onLine func(string)) (string, string, int, error) {
		var joined string
		for _, l := range lines {
			if onLine != nil {
				onLine(l)
			}
			joined += l + "\n"
		}
		return joined, stderr, exitCode, err
	}
}

// echoParser is a trivial StreamParser: every non-blank line becomes one
// assistant message event, and Result concatenates them so a fenced result
// envelope embedded across the lines is still recoverable.
type echoParser struct{ acc string }

func (p *echoParser) Line(line string) []agent.TranscriptEvent {
	if line == "" {
		return nil
	}
	p.acc += line + "\n"
	return []agent.TranscriptEvent{{Type: agent.TranscriptEventMessage, Role: "assistant", Text: line}}
}

func (p *echoParser) Result() string { return p.acc }

// A subprocess error must never leave a blank transcript (issue #257): the
// captured stderr/stdout is persisted as a fallback event so a failed run is
// diagnosable.
func TestExecuteCLI_SubprocessErrorStillPersistsTranscript(t *testing.T) {
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("", "error: unexpected argument '--full-auto' found", 2, errors.New("exec failed")),
	}
	sink := agent.NewTranscriptRecorder()
	res, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{Transcript: sink})
	if err != nil {
		t.Fatalf("ExecuteCLI returned error %v, want nil", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
	events := sink.Events()
	if len(events) == 0 {
		t.Fatalf("Events() = 0, want a non-blank fallback transcript on failure")
	}
	found := false
	for _, e := range events {
		if strings.Contains(e.Text, "--full-auto") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback transcript did not carry the captured stderr diagnostic: %+v", events)
	}
}

// A cancelled run must also persist whatever it captured, not go blank.
func TestExecuteCLI_CancellationStillPersistsTranscript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := CLIConfig{
		BackendName: "codex",
		Runner:      fixedRunner("partial output before kill", "", -1, context.Canceled),
	}
	sink := agent.NewTranscriptRecorder()
	_, err := ExecuteCLI(ctx, cfg, agent.AgentRequest{Transcript: sink})
	if err == nil {
		t.Fatalf("ExecuteCLI: want a wrapped context error, got nil")
	}
	if len(sink.Events()) == 0 {
		t.Fatalf("Events() = 0, want the pre-kill output persisted")
	}
}

// With a StreamParser, events are emitted per line as they arrive (not one
// coarse blob), and the result envelope is read from the parser's
// reconstructed text.
func TestExecuteCLI_StreamParserEmitsPerLineAndParsesResult(t *testing.T) {
	lines := []string{
		"working on it",
		"almost done",
		"```json",
		`{"status":"IMPLEMENTED","summary":"streamed"}`,
		"```",
	}
	cfg := CLIConfig{
		BackendName:     "codex",
		Runner:          lineRunner(lines, "", 0, nil),
		NewStreamParser: func() StreamParser { return &echoParser{} },
	}
	sink := agent.NewTranscriptRecorder()
	res, err := ExecuteCLI(context.Background(), cfg, agent.AgentRequest{Transcript: sink})
	if err != nil {
		t.Fatalf("ExecuteCLI: %v", err)
	}
	if res.Status != agent.StatusImplemented || res.Summary != "streamed" {
		t.Fatalf("result = %+v, want IMPLEMENTED/streamed parsed from the stream", res)
	}
	// One event per non-blank streamed line — proof of incremental capture,
	// not a single terminal emit.
	if got := len(sink.Events()); got != len(lines) {
		t.Fatalf("Events() = %d, want %d (one per streamed line)", got, len(lines))
	}
}
