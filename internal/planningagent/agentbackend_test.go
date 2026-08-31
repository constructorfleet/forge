package planningagent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
)

func TestAgentBackendInvoke_BuildsModeStructuredRequest(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{Summary: `{"ok":true}`})

	backend := NewAgentBackend(fake)

	raw, err := backend.Invoke(context.Background(), InvokeRequest{
		Key:    "goal-draft",
		Prompt: "draft a goal",
		Schema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if raw != `{"ok":true}` {
		t.Fatalf("raw = %q, want the AgentResult.Summary verbatim", raw)
	}

	invocations := fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("len(invocations) = %d, want 1", len(invocations))
	}
	req := invocations[0]
	if req.Mode != agent.ModeStructured {
		t.Fatalf("Mode = %q, want %q", req.Mode, agent.ModeStructured)
	}
	if req.Prompt != "draft a goal" {
		t.Fatalf("Prompt = %q, want verbatim InvokeRequest.Prompt", req.Prompt)
	}
	if req.Schema != `{"type":"object"}` {
		t.Fatalf("Schema = %q, want the per-call InvokeRequest.Schema", req.Schema)
	}
}

func TestAgentBackendInvoke_RunsInIsolatedWorkingDirectory(t *testing.T) {
	fake := agent.NewFakeAgent()
	fake.ProgramDefault(agent.AgentResult{Summary: `{}`})

	backend := NewAgentBackend(fake)

	if _, err := backend.Invoke(context.Background(), InvokeRequest{Key: "k", Prompt: "p"}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("len(invocations) = %d, want 1", len(invocations))
	}
	workspace := invocations[0].WorkspacePath
	if workspace == "" {
		t.Fatalf("WorkspacePath is empty, want an isolated temp directory")
	}
	if wd, err := os.Getwd(); err == nil && workspace == wd {
		t.Fatalf("WorkspacePath = %q, want isolated from the working directory %q", workspace, wd)
	}
	if !strings.HasPrefix(workspace, os.TempDir()) {
		t.Fatalf("WorkspacePath = %q, want a temp directory under %q", workspace, os.TempDir())
	}
	if entries, err := os.ReadDir(workspace); err != nil {
		t.Fatalf("ReadDir(%q): %v", workspace, err)
	} else if len(entries) != 0 {
		t.Fatalf("workspace %q is not empty: %v", workspace, entries)
	}
}

func TestAgentBackendInvoke_SurfacesExecuteError(t *testing.T) {
	fake := agent.NewFakeAgent()
	wantErr := errors.New("backend exploded")
	fake.ProgramError("k", wantErr)

	backend := NewAgentBackend(fake)

	_, err := backend.Invoke(context.Background(), InvokeRequest{Key: "k", Prompt: "p"})
	if err == nil {
		t.Fatalf("Invoke: want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Invoke error = %v, want wrapping %v", err, wantErr)
	}
}
