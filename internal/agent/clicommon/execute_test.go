package clicommon

import (
	"context"
	"errors"
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
