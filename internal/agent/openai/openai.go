// Package openai implements two OpenAI-compatible Agent Adapters (see
// CONTEXT.md "Agent Adapter"): ResponsesAdapter (the Responses API) and
// ChatCompletionsAdapter (the Chat Completions API). Both invoke an
// OpenAI-compatible HTTP endpoint rather than shelling out to a CLI, so —
// unlike internal/agent/codex, internal/agent/opencode, and
// internal/agent/pi — they do not build on internal/agent/clicommon's
// subprocess mechanics, but they do share its backend-agnostic prompt and
// result-envelope contract, mirroring internal/agent/claude's conventions
// for everything else (sanitized auth, bounded diagnostics, best-effort
// transcript capture).
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/clicommon"
)

// defaultAPIKeyEnvVar is the environment variable an Adapter reads its
// bearer credential from when APIKeyEnvVar is empty. The credential is
// looked up at call time (like internal/tracker/github.Client's token
// lookup) rather than captured into config, so it is never persisted
// alongside workflow state.
const defaultAPIKeyEnvVar = "OPENAI_API_KEY"

// apiKey resolves the bearer credential for an Adapter from envVar (or
// defaultAPIKeyEnvVar if empty).
func apiKey(envVar string) string {
	if envVar == "" {
		envVar = defaultAPIKeyEnvVar
	}
	return os.Getenv(envVar)
}

// httpDoer is the subset of *http.Client an Adapter needs, letting tests
// inject a client pointed at an httptest.Server (see
// internal/tracker/github.Client's httpClient field for the same pattern).
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// postJSON issues a POST request with reqBody marshaled as JSON, resolves
// bearer authentication from apiKeyEnvVar, and returns the raw response
// body alongside the status code. Ordinary HTTP-layer failures (transport
// errors, non-2xx statuses) are returned as plain errors for the caller to
// fold into an AgentResult; ctx cancellation is the caller's responsibility
// to check separately (mirroring internal/agent/clicommon.ExecuteCLI's
// error-classification order).
func postJSON(ctx context.Context, client httpDoer, url, apiKeyEnvVar string, reqBody interface{}) (statusCode int, respBody []byte, err error) {
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("openai: encode request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(encoded)))
	if err != nil {
		return 0, nil, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key := apiKey(apiKeyEnvVar); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("openai: request %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("openai: read response %s: %w", url, err)
	}
	return resp.StatusCode, body, nil
}

// requestContext bounds one whole request to d (issue #455). Both Adapters
// read a complete, non-streaming response, so there is no output stream to
// reset an idle deadline against — unlike the CLI Adapters' idle timeout,
// the bound here is the total request duration. d <= 0 disables the bound.
func requestContext(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// timedOutResult reports the timeout of a request bounded by requestContext
// as an ordinary FAILED outcome (err == nil), matching every other Agent
// Adapter: a stalled provider is what the retry budget exists to recover
// from, while an operator-driven cancellation aborts the run instead. It
// returns handled == false when reqCtx did not exceed its own deadline.
func timedOutResult(backendName string, reqCtx context.Context, d time.Duration) (res agent.AgentResult, handled bool) {
	if !errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return agent.AgentResult{}, false
	}
	return failedResult(fmt.Sprintf("%s adapter: agent timed out after %s (bounds the whole request, not an idle period)", backendName, d), ""), true
}

// buildResult resolves backendName's extracted response text into an
// agent.AgentResult, delegating structured-result parsing to
// internal/agent/clicommon (the same contract every Agent Adapter shares)
// and preferring apiUsage — the token accounting the API response itself
// reported — over any usage the model self-reported inside its structured
// result, since the former is authoritative.
func buildResult(backendName, text string, apiUsage *agent.TokenUsage) agent.AgentResult {
	structured, ok := clicommon.ParseStructuredResult(text)
	res := clicommon.Resolve(backendName, structured, ok, 0, text, "")
	if apiUsage != nil {
		res.Usage = apiUsage
	}
	return res
}

// emitTranscript records text as a single best-effort assistant message on
// req.Transcript, when set, matching internal/agent/clicommon.ExecuteCLI's
// coarse (whole-response, not per-turn) transcript granularity for
// non-streaming backends.
func emitTranscript(req agent.AgentRequest, text string) {
	if req.Transcript == nil || text == "" {
		return
	}
	req.Transcript.Emit(agent.TranscriptEvent{
		Type: agent.TranscriptEventMessage,
		Role: "assistant",
		Text: clicommon.Truncate(text, clicommon.MaxDiagnosticLen),
	})
}

// failedResult builds a FAILED AgentResult carrying bounded diagnostics,
// matching internal/agent/clicommon.DiagnosticSummary's shape.
func failedResult(prefix, body string) agent.AgentResult {
	return agent.AgentResult{
		Status:  agent.StatusFailed,
		Summary: clicommon.DiagnosticSummary(prefix, body, ""),
	}
}
