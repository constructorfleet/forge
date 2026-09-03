package planningagent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/storage"
)

// transcriptAgent is an agent.Agent that emits a scripted transcript into
// req.Transcript before returning, which agent.FakeAgent deliberately does
// not do (it only records requests). Planning transcript persistence is
// exactly the wiring between req.Transcript and storage, so the double has
// to exercise the sink.
type transcriptAgent struct {
	events   []agent.TranscriptEvent
	result   agent.AgentResult
	err      error
	captured []agent.AgentRequest
}

func (a *transcriptAgent) Execute(_ context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	a.captured = append(a.captured, req)
	if req.Transcript != nil {
		for _, event := range a.events {
			req.Transcript.Emit(event)
		}
	}
	return a.result, a.err
}

func openPersistenceStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func TestPersistingAgentBackendInvoke_RecordsTranscriptEvents(t *testing.T) {
	store := openPersistenceStore(t)
	ctx := context.Background()

	occurred := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	ag := &transcriptAgent{
		events: []agent.TranscriptEvent{
			{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "checking the spec", Timestamp: occurred},
			{Type: agent.TranscriptEventToolCall, Role: "assistant", ToolName: "Read", ToolInput: "spec.md", ToolCallID: "call-1", Timestamp: occurred},
		},
		result: agent.AgentResult{Status: agent.StatusImplemented, Summary: `{"ok":true}`},
	}

	backend := NewPersistingAgentBackend(ag, store, "feature-x", "feature-x")

	raw, err := backend.Invoke(ctx, InvokeRequest{
		Key:    "specification-review",
		Prompt: "review the spec",
		Schema: []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if raw != `{"ok":true}` {
		t.Fatalf("raw = %q, want the AgentResult.Summary verbatim", raw)
	}

	events, err := store.TranscriptEventsByIssue(ctx, "feature-x", "feature-x")
	if err != nil {
		t.Fatalf("TranscriptEventsByIssue: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d transcript events, want 2: %+v", len(events), events)
	}
	for _, event := range events {
		if event.Phase != "planning" {
			t.Errorf("Phase = %q, want %q", event.Phase, "planning")
		}
		if event.Subagent != "specification-review" {
			t.Errorf("Subagent = %q, want the InvokeRequest.Key", event.Subagent)
		}
	}
	if events[0].Text != "checking the spec" || events[1].ToolName != "Read" || events[1].ToolCallID != "call-1" {
		t.Fatalf("persisted events = %+v, want the emitted transcript", events)
	}

	runs, err := store.AgentRunsByIssue(ctx, "feature-x", "feature-x")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d agent runs, want 1", len(runs))
	}
	if runs[0].Result != string(agent.StatusImplemented) {
		t.Fatalf("agent run Result = %q, want %q", runs[0].Result, agent.StatusImplemented)
	}
	if runs[0].Backend == "" {
		t.Fatal("agent run Backend is empty, want a descriptive planning backend name")
	}
	if runs[0].ContextBytes <= 0 {
		t.Fatalf("agent run ContextBytes = %d, want positive", runs[0].ContextBytes)
	}
}

// A transcript is only durable if it survives the failure it is most needed
// for, so events emitted before an Execute error must still be persisted and
// the run still finalized -- while the error itself still surfaces.
func TestPersistingAgentBackendInvoke_PersistsTranscriptOnExecuteError(t *testing.T) {
	store := openPersistenceStore(t)
	ctx := context.Background()

	wantErr := errors.New("backend exploded")
	ag := &transcriptAgent{
		events: []agent.TranscriptEvent{
			{Type: agent.TranscriptEventMessage, Role: "assistant", Text: "starting", Timestamp: time.Now().UTC()},
		},
		err: wantErr,
	}

	backend := NewPersistingAgentBackend(ag, store, "feature-x", "feature-x")

	if _, err := backend.Invoke(ctx, InvokeRequest{Key: "ticket-plan-review", Prompt: "p"}); !errors.Is(err, wantErr) {
		t.Fatalf("Invoke error = %v, want it to wrap %v", err, wantErr)
	}

	events, err := store.TranscriptEventsByIssue(ctx, "feature-x", "feature-x")
	if err != nil {
		t.Fatalf("TranscriptEventsByIssue: %v", err)
	}
	if len(events) != 1 || events[0].Subagent != "ticket-plan-review" {
		t.Fatalf("persisted events = %+v, want the pre-error transcript tagged ticket-plan-review", events)
	}

	runs, err := store.AgentRunsByIssue(ctx, "feature-x", "feature-x")
	if err != nil {
		t.Fatalf("AgentRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Result != "ERROR" {
		t.Fatalf("agent runs = %+v, want one finalized ERROR run", runs)
	}
}

// Persistence is best-effort, exactly as internal/engine documents it: a
// storage failure is a durability gap, never a reason to fail the planning
// invocation in progress.
func TestPersistingAgentBackendInvoke_SurvivesStoreFailures(t *testing.T) {
	ag := &transcriptAgent{
		events: []agent.TranscriptEvent{{Type: agent.TranscriptEventMessage, Text: "hi", Timestamp: time.Now().UTC()}},
		result: agent.AgentResult{Status: agent.StatusImplemented, Summary: `{"ok":true}`},
	}

	backend := NewPersistingAgentBackend(ag, failingTranscriptStore{}, "feature-x", "feature-x")

	raw, err := backend.Invoke(context.Background(), InvokeRequest{Key: "specification-review", Prompt: "p"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if raw != `{"ok":true}` {
		t.Fatalf("raw = %q, want the AgentResult.Summary verbatim", raw)
	}
	if len(ag.captured) != 1 {
		t.Fatalf("len(captured) = %d, want 1", len(ag.captured))
	}
	if ag.captured[0].Transcript == nil {
		t.Fatal("Transcript is nil, want a usable sink even when StartAgentRun fails")
	}
}

type failingTranscriptStore struct{}

func (failingTranscriptStore) StartAgentRun(context.Context, storage.AgentRun) (int64, error) {
	return 0, errors.New("store down")
}

func (failingTranscriptStore) FinalizeAgentRun(context.Context, int64, storage.AgentRun) error {
	return errors.New("store down")
}

func (failingTranscriptStore) RecordTranscriptEvents(context.Context, string, string, int64, []storage.TranscriptEvent) error {
	return errors.New("store down")
}

// NewAgentBackend keeps its no-persistence behaviour: existing callers that
// have no Store must still get a nil req.Transcript (no capture, no writes).
func TestNewAgentBackendInvoke_DoesNotPersist(t *testing.T) {
	ag := &transcriptAgent{result: agent.AgentResult{Summary: `{}`}}

	if _, err := NewAgentBackend(ag).Invoke(context.Background(), InvokeRequest{Key: "k", Prompt: "p"}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(ag.captured) != 1 {
		t.Fatalf("len(captured) = %d, want 1", len(ag.captured))
	}
	if ag.captured[0].Transcript != nil {
		t.Fatalf("Transcript = %#v, want nil without a Store", ag.captured[0].Transcript)
	}
}
