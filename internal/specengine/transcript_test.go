package specengine_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/storage"
)

// scriptedTranscriptAgent is an agent.Agent that returns a canned
// ModeStructured Summary per invocation key (AgentBackend threads
// InvokeRequest.Key through as Issue.ID) and emits one transcript message
// per call. planningagent.FakeBackend cannot stand in here: it short-circuits
// the agent.Agent entirely, and the agent.Agent boundary -- specifically
// req.Transcript -- is exactly what this test is about.
type scriptedTranscriptAgent struct {
	summaries map[string]string
}

func (a *scriptedTranscriptAgent) Execute(_ context.Context, req agent.AgentRequest) (agent.AgentResult, error) {
	if req.Transcript != nil {
		req.Transcript.Emit(agent.TranscriptEvent{
			Type:      agent.TranscriptEventMessage,
			Role:      "assistant",
			Text:      "working on " + req.Issue.ID,
			Timestamp: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
		})
	}
	return agent.AgentResult{Status: agent.StatusImplemented, Summary: a.summaries[req.Issue.ID]}, nil
}

// TestSpecEngineGenerateSpec_PersistsPlanningTranscripts is issue #248's
// acceptance criterion end to end: driving `forge plan`'s spec stage through
// the production planningagent.AgentBackend against a real store must leave
// the SpecificationReview invocation's transcript in transcript_events,
// tagged phase="planning" and subagent="specification-review".
func TestSpecEngineGenerateSpec_PersistsPlanningTranscripts(t *testing.T) {
	ctx := context.Background()

	store, err := storage.Open(filepath.Join(t.TempDir(), "forge.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	loader := &fakeLoaderForTest{
		goal: &planning.Artifact{
			Kind:     planning.KindGoal,
			Revision: "goal-rev",
			State:    "approved",
			Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
		},
	}

	ag := &scriptedTranscriptAgent{summaries: map[string]string{
		"specification-generation": `{
			"summary": "A widget builder",
			"requirements": [{"id": "REQ-001", "description": "Widget must be buildable"}],
			"non_goals": [],
			"decision_refs": []
		}`,
		"specification-review": `{
			"verdict": "APPROVED",
			"summary": "Specification is clear and complete",
			"findings": []
		}`,
	}}

	// featureID doubles as execution_id and issue_id, matching how
	// cmd/forge's buildPlanningBackend scopes planning transcripts.
	const featureID = "widget"
	backend := planningagent.NewPersistingAgentBackend(ag, store, featureID, featureID)

	engine := specengine.NewSpecEngine(backend)
	if err := engine.GenerateSpec(ctx, featureID, loader); err != nil {
		t.Fatalf("GenerateSpec: %v", err)
	}
	if loader.spec == nil {
		t.Fatal("spec not saved; the review stage never approved")
	}

	events, err := store.TranscriptEventsByIssue(ctx, featureID, featureID)
	if err != nil {
		t.Fatalf("TranscriptEventsByIssue: %v", err)
	}

	var reviewEvents int
	for _, event := range events {
		if event.Phase != "planning" {
			t.Errorf("event %+v has Phase %q, want %q", event, event.Phase, "planning")
		}
		if event.Subagent == "specification-review" {
			reviewEvents++
			if event.Text != "working on specification-review" {
				t.Errorf("spec-review event Text = %q, want the emitted transcript message", event.Text)
			}
		}
	}
	if reviewEvents == 0 {
		t.Fatalf("no transcript_events tagged subagent=specification-review; got %+v", events)
	}

	// The generation stage flows through the same seam, so its transcript
	// must be there too, distinguishable by subagent alone.
	var generationEvents int
	for _, event := range events {
		if event.Subagent == "specification-generation" {
			generationEvents++
		}
	}
	if generationEvents == 0 {
		t.Fatalf("no transcript_events tagged subagent=specification-generation; got %+v", events)
	}
}
