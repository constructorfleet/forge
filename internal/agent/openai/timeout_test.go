package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
)

// hangingServer never answers, modelling a stalled endpoint.
func hangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

func TestResponsesAdapter_TimeoutBoundsStalledRequest(t *testing.T) {
	srv := hangingServer(t)
	a := &ResponsesAdapter{BaseURL: srv.URL, Timeout: 30 * time.Millisecond}

	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (a timeout is an ordinary FAILED outcome)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
	if !strings.Contains(res.Summary, "timed out") {
		t.Fatalf("Summary = %q, want it to report a timeout", res.Summary)
	}
}

func TestChatCompletionsAdapter_TimeoutBoundsStalledRequest(t *testing.T) {
	srv := hangingServer(t)
	a := &ChatCompletionsAdapter{BaseURL: srv.URL, Timeout: 30 * time.Millisecond}

	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (a timeout is an ordinary FAILED outcome)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
	if !strings.Contains(res.Summary, "timed out") {
		t.Fatalf("Summary = %q, want it to report a timeout", res.Summary)
	}
}

// A canceled parent context stays a cancellation, not an adapter timeout.
func TestResponsesAdapter_ParentCancellationIsNotATimeout(t *testing.T) {
	srv := hangingServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &ResponsesAdapter{BaseURL: srv.URL, Timeout: time.Minute}

	res, err := a.Execute(ctx, agent.AgentRequest{})
	if err == nil {
		t.Fatalf("Execute: want a wrapped context error, got nil")
	}
	if strings.Contains(res.Summary, "timed out") {
		t.Fatalf("Summary = %q, want a cancellation, not an adapter timeout", res.Summary)
	}
}
