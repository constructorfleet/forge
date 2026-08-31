package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

func newChatCompletionsTestServer(t *testing.T, status int, body string, capture *capturedRequest) *httptest.Server {
	t.Helper()
	return newResponsesTestServer(t, status, body, capture)
}

func TestChatCompletionsAdapter_ExecuteImplementedResult(t *testing.T) {
	respBody := `{
		"choices": [{"message": {"role": "assistant", "content": "some narration\n` + "```json\\n{\\\"status\\\":\\\"IMPLEMENTED\\\",\\\"summary\\\":\\\"done\\\"}\\n```" + `"}}],
		"usage": {"prompt_tokens": 3, "completion_tokens": 4}
	}`
	var captured capturedRequest
	srv := newChatCompletionsTestServer(t, http.StatusOK, respBody, &captured)
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	a := &ChatCompletionsAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}

	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != agent.StatusImplemented || res.Summary != "done" {
		t.Fatalf("res = %+v, want IMPLEMENTED/done", res)
	}
	if res.Usage == nil || res.Usage.InputTokens != 3 || res.Usage.OutputTokens != 4 {
		t.Fatalf("res.Usage = %+v, want input=3 output=4 from API-reported usage", res.Usage)
	}
	if captured.path != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", captured.path)
	}
	if captured.authHeader != "Bearer test-key" {
		t.Fatalf("authHeader = %q, want Bearer test-key", captured.authHeader)
	}
	messages, ok := captured.body["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("body[messages] = %v, want a single-message array", captured.body["messages"])
	}
}

func TestChatCompletionsAdapter_ExecuteHTTPErrorStatusIsFailedWithoutGoError(t *testing.T) {
	srv := newChatCompletionsTestServer(t, http.StatusInternalServerError, `{"error":"boom"}`, nil)
	defer srv.Close()

	a := &ChatCompletionsAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (ordinary failures surface via Status)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestChatCompletionsAdapter_ExecuteContextCancellationSurfacesError(t *testing.T) {
	srv := newChatCompletionsTestServer(t, http.StatusOK, `{}`, nil)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a := &ChatCompletionsAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	res, err := a.Execute(ctx, agent.AgentRequest{})
	if err == nil {
		t.Fatalf("Execute: want a wrapped context error, got nil")
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestChatCompletionsAdapter_ExecuteNoStructuredResultIsFailed(t *testing.T) {
	respBody := `{"choices": [{"message": {"role": "assistant", "content": "no json here"}}]}`
	srv := newChatCompletionsTestServer(t, http.StatusOK, respBody, nil)
	defer srv.Close()

	a := &ChatCompletionsAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

// A failed request (non-2xx) must still persist a diagnostic transcript
// event — no run is ever a blank transcript (issue #257).
func TestChatCompletionsAdapter_ExecuteFailureNeverBlankTranscript(t *testing.T) {
	srv := newChatCompletionsTestServer(t, http.StatusInternalServerError, `{"error":"boom"}`, nil)
	defer srv.Close()

	a := &ChatCompletionsAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	sink := agent.NewTranscriptRecorder()
	res, err := a.Execute(context.Background(), agent.AgentRequest{Transcript: sink})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
	if len(sink.Events()) == 0 {
		t.Fatalf("Events() = 0, want a non-blank fallback transcript on failure")
	}
}
