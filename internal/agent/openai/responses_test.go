package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

func newResponsesTestServer(t *testing.T, status int, body string, capture *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture.method = r.Method
			capture.path = r.URL.Path
			capture.authHeader = r.Header.Get("Authorization")
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			capture.body = payload
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

type capturedRequest struct {
	method     string
	path       string
	authHeader string
	body       map[string]interface{}
}

func TestResponsesAdapter_ExecuteImplementedResult(t *testing.T) {
	respBody := `{
		"output": [{"content": [{"type": "output_text", "text": "some narration\n` + "```json\\n{\\\"status\\\":\\\"IMPLEMENTED\\\",\\\"summary\\\":\\\"done\\\"}\\n```" + `"}]}],
		"usage": {"input_tokens": 5, "output_tokens": 7}
	}`
	var captured capturedRequest
	srv := newResponsesTestServer(t, http.StatusOK, respBody, &captured)
	defer srv.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	a := &ResponsesAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}

	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != agent.StatusImplemented || res.Summary != "done" {
		t.Fatalf("res = %+v, want IMPLEMENTED/done", res)
	}
	if res.Usage == nil || res.Usage.InputTokens != 5 || res.Usage.OutputTokens != 7 {
		t.Fatalf("res.Usage = %+v, want input=5 output=7 from API-reported usage", res.Usage)
	}
	if captured.path != "/responses" {
		t.Fatalf("path = %q, want /responses", captured.path)
	}
	if captured.authHeader != "Bearer test-key" {
		t.Fatalf("authHeader = %q, want Bearer test-key", captured.authHeader)
	}
	if captured.body["model"] != "gpt-test" {
		t.Fatalf("body[model] = %v, want gpt-test", captured.body["model"])
	}
}

func TestResponsesAdapter_ExecuteHTTPErrorStatusIsFailedWithoutGoError(t *testing.T) {
	srv := newResponsesTestServer(t, http.StatusInternalServerError, `{"error":"boom"}`, nil)
	defer srv.Close()

	a := &ResponsesAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (ordinary failures surface via Status)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestResponsesAdapter_ExecuteContextCancellationSurfacesError(t *testing.T) {
	srv := newResponsesTestServer(t, http.StatusOK, `{}`, nil)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a := &ResponsesAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	res, err := a.Execute(ctx, agent.AgentRequest{})
	if err == nil {
		t.Fatalf("Execute: want a wrapped context error, got nil")
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

func TestResponsesAdapter_ExecuteNoStructuredResultIsFailed(t *testing.T) {
	respBody := `{"output": [{"content": [{"type": "output_text", "text": "no json here"}]}]}`
	srv := newResponsesTestServer(t, http.StatusOK, respBody, nil)
	defer srv.Close()

	a := &ResponsesAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Model: "gpt-test"}
	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}
