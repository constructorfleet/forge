package engine_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

// statusReflectionConfig returns a config.Default() with ticket 24's
// status-reflection block opted in, so these tests exercise the same
// defaults an operator would get by only flipping status_reflection.enabled
// to true.
func statusReflectionConfig() config.Config {
	cfg := config.Default()
	cfg.StatusReflection = config.StatusReflectionConfig{
		Enabled:         true,
		InProgressLabel: "in-progress",
		InReviewLabel:   "in-review",
		FailedLabel:     "failed",
		Comment:         true,
	}
	return cfg
}

func newStatusReflectionTestEngine(t *testing.T, cfg config.Config, issues map[string]domain.Issue) (*engine.Engine, *fakeTracker, *agent.FakeAgent, string) {
	t.Helper()
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := newFakeTracker()
	for id, issue := range issues {
		trk.issues[id] = issue
	}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := agent.NewFakeAgent()
	eng := engine.New(store, trk, mgr, fake, cfg, repoRoot)
	eng.StatusTracker = trk
	return eng, trk, fake, base
}

// TestExecute_StatusReflection_Disabled_NoTrackerSideEffects pins the
// default-off behavior: with cfg.StatusReflection.Enabled left at its
// config.Default() value (false), a StatusTracker being wired changes
// nothing observable on the tracker even though the Issue moves all the way
// through CLAIMED and beyond.
func TestExecute_StatusReflection_Disabled_NoTrackerSideEffects(t *testing.T) {
	eng, trk, fake, base := newStatusReflectionTestEngine(t, config.Default(), map[string]domain.Issue{
		"1": {ID: "1"},
	})
	fake.ProgramResult("1", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	if _, err := eng.Execute(context.Background(), "1", base); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 0 {
		t.Errorf("labels = %v, want none (status_reflection disabled)", labels)
	}
	if n := trk.CommentCount("1"); n != 0 {
		t.Errorf("comment count = %d, want 0 (status_reflection disabled)", n)
	}
}

// TestExecute_StatusReflection_AppliesInProgressLabelAndStartComment covers
// ticket 24's first checklist item: on the transition into active work,
// Forge applies the configured in-progress label and (Comment: true) posts
// a one-time start comment.
func TestExecute_StatusReflection_AppliesInProgressLabelAndStartComment(t *testing.T) {
	eng, trk, fake, base := newStatusReflectionTestEngine(t, statusReflectionConfig(), map[string]domain.Issue{
		"1": {ID: "1"},
	})
	fake.ProgramResult("1", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	result, err := eng.Execute(context.Background(), "1", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// No Reviewer configured, so review auto-approves to COMMITTING; no
	// Publisher/PRTracker configured, so runCommitAndPR no-ops and the run
	// rests in COMMITTING — still within the in-progress range
	// (statusreflect.Label).
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "in-progress" {
		t.Errorf("labels = %v, want [in-progress]", labels)
	}
	if n := trk.CommentCount("1"); n != 1 {
		t.Errorf("comment count = %d, want 1 (start comment posted once)", n)
	}
}

// TestExecute_StatusReflection_FailedAppliesFailedLabel covers the FAILED
// checklist item: the in-progress label is replaced with the configured
// failed label once an Issue reaches FAILED.
func TestExecute_StatusReflection_FailedAppliesFailedLabel(t *testing.T) {
	eng, trk, fake, base := newStatusReflectionTestEngine(t, statusReflectionConfig(), map[string]domain.Issue{
		"1": {ID: "1"},
	})
	fake.ProgramResult("1", agent.AgentResult{Status: agent.StatusFailed, Summary: "could not implement"})

	result, err := eng.Execute(context.Background(), "1", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "failed" {
		t.Errorf("labels = %v, want [failed]", labels)
	}
}

// TestExecute_StatusReflection_NeedsInfoComposesWithBlockedLabel covers the
// NEEDS_INFO checklist item: the status-reflection in-progress label is
// removed, leaving only the existing blocked/needs-info label — the two
// features compose rather than fight.
func TestExecute_StatusReflection_NeedsInfoComposesWithBlockedLabel(t *testing.T) {
	cfg := statusReflectionConfig()
	eng, trk, fake, base := newStatusReflectionTestEngine(t, cfg, map[string]domain.Issue{
		"1": {ID: "1"},
	})
	eng.NeedsInfoTracker = trk
	fake.ProgramResult("1", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which flag?"},
	})

	result, err := eng.Execute(context.Background(), "1", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
	}

	wantLabel := cfg.Blocked.Label
	labels := trk.Labels("1")
	if len(labels) != 1 || labels[0] != wantLabel {
		t.Errorf("labels = %v, want [%s] only (in-progress removed, blocked label composes)", labels, wantLabel)
	}
}

// TestExecute_StatusReflection_RetryDoesNotDoublePostStartComment covers
// ticket 24's idempotency checklist item for the one side effect that is
// not naturally idempotent (AddComment has no tracker-side dedup key): a
// second READY -> CLAIMED transition for the same Execution/Issue (RetryIssue
// after FAILED) must not post the start comment again.
func TestExecute_StatusReflection_RetryDoesNotDoublePostStartComment(t *testing.T) {
	eng, trk, fake, base := newStatusReflectionTestEngine(t, statusReflectionConfig(), map[string]domain.Issue{
		"1": {ID: "1"},
	})
	// Both outcomes are queued up front: agent.FakeAgent's OutcomeQueue
	// repeats the final queued outcome once only one remains, so a second
	// ProgramResult call issued after the first Execute would just extend
	// the still-repeating first entry rather than supply a fresh one.
	fake.ProgramResult("1", agent.AgentResult{Status: agent.StatusFailed, Summary: "could not implement"})
	fake.ProgramResult("1", agent.AgentResult{Status: agent.StatusImplemented, Summary: "fixed it"})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "1", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if n := trk.CommentCount("1"); n != 1 {
		t.Fatalf("comment count after first run = %d, want 1", n)
	}

	if _, err := eng.RetryIssue(ctx, result.ExecutionID, "1"); err != nil {
		t.Fatalf("RetryIssue: %v", err)
	}

	if n := trk.CommentCount("1"); n != 1 {
		t.Errorf("comment count after retry = %d, want 1 (no double-post)", n)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "in-progress" {
		t.Errorf("labels after retry = %v, want [in-progress]", labels)
	}
}
