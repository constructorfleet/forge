package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
)

type capturedCall struct {
	dir   string
	args  []string
	stdin string
	env   []string
}

// fixedRunner returns a Runner that streams agentMessage as an opencode
// `run --format json` text-part event (the shape the adapter's streamParser
// consumes) through onLine, and returns it as the full captured stdout —
// mirroring DefaultRunner's contract now that opencode runs in --format json
// mode.
func fixedRunner(agentMessage string) (Runner, *capturedCall) {
	call := &capturedCall{}
	ev, _ := json.Marshal(opencodeEvent{
		Type: "text",
		Part: &opencodePart{Type: "text", Text: agentMessage},
	})
	line := string(ev)
	return func(_ context.Context, dir string, args []string, stdin string, env []string, onLine func(string)) (string, string, int, error) {
		call.dir = dir
		call.args = args
		call.stdin = stdin
		call.env = env
		if onLine != nil {
			onLine(line)
		}
		return line + "\n", "", 0, nil
	}, call
}

func TestAdapter_ExecuteDelegatesToOpencodeCLI(t *testing.T) {
	runner, call := fixedRunner("```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"done\"}\n```\n")
	a := &Adapter{Runner: runner}

	res, err := a.Execute(context.Background(), agent.AgentRequest{WorkspacePath: "/workspace"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != agent.StatusImplemented || res.Summary != "done" {
		t.Fatalf("res = %+v, want IMPLEMENTED/done", res)
	}
	if call.dir != "/workspace" {
		t.Fatalf("dir = %q, want /workspace", call.dir)
	}
	if call.stdin == "" {
		t.Fatalf("stdin was empty, want the rendered prompt")
	}
}

func TestAdapter_ExecutePassesConfiguredArgs(t *testing.T) {
	runner, call := fixedRunner("```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"ok\"}\n```\n")
	a := &Adapter{Runner: runner}

	if _, err := a.Execute(context.Background(), agent.AgentRequest{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(call.args) == 0 {
		t.Fatalf("args = %v, want non-empty non-interactive flags", call.args)
	}
}

func TestAdapter_ExecuteSanitizesEnv(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "secret")
	t.Setenv("FORGE_OPENCODE_TEST_UNRELATED", "should-not-pass")

	runner, call := fixedRunner("```json\n{\"status\":\"IMPLEMENTED\",\"summary\":\"ok\"}\n```\n")
	a := &Adapter{Runner: runner}

	if _, err := a.Execute(context.Background(), agent.AgentRequest{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	foundKey := false
	for _, e := range call.env {
		if e == "OPENCODE_API_KEY=secret" {
			foundKey = true
		}
		if e == "FORGE_OPENCODE_TEST_UNRELATED=should-not-pass" {
			t.Fatalf("env leaked unrelated variable: %v", call.env)
		}
	}
	if !foundKey {
		t.Fatalf("env = %v, want OPENCODE_API_KEY passed through", call.env)
	}
}

func TestAdapter_ExecuteSubprocessErrorIsFailedWithoutGoError(t *testing.T) {
	a := &Adapter{
		Runner: func(context.Context, string, []string, string, []string, func(string)) (string, string, int, error) {
			return "", "boom", 1, context.DeadlineExceeded
		},
	}
	res, err := a.Execute(context.Background(), agent.AgentRequest{})
	if err != nil {
		t.Fatalf("Execute returned error %v, want nil (ordinary failures surface via Status)", err)
	}
	if res.Status != agent.StatusFailed {
		t.Fatalf("Status = %v, want FAILED", res.Status)
	}
}

// TestAdapterTimeoutBoundsWedgedRun asserts the configured agent timeout
// reaches this adapter, so a wedged, output-free run cannot hang forever
// (issue #455).
func TestAdapterTimeoutBoundsWedgedRun(t *testing.T) {
	a := &Adapter{
		Timeout: 20 * time.Millisecond,
		Runner: func(ctx context.Context, _ string, _ []string, _ string, _ []string, _ func(string)) (string, string, int, error) {
			<-ctx.Done()
			return "", "", -1, ctx.Err()
		},
	}
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
