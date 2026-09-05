package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/workspace"
)

// TestResume_NewHumanComment_TransitionsNeedsInfoToReady is the ticket's
// resume half of the headline integration test: after an Issue enters
// NEEDS_INFO, a new human comment posted after the checkpoint's timestamp
// causes Resume to move it to READY, with a focused resumed context
// (original issue + previous question + only the new comment).
func TestResume_NewHumanComment_TransitionsNeedsInfoToReady(t *testing.T) {
	eng, store, trk, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7"},
	})
	fake.ProgramResult("7", agent.AgentResult{
		Status: agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: "which config flag?",
			Context:  "ambiguous flags",
		},
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "7", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", result.Issue.State)
	}

	// A human posts an old-looking comment predating the checkpoint (there
	// is none here since the checkpoint was just written) plus one clearly
	// after it.
	trk.AddHumanComment("7", "alice", "use FORGE_FOO", time.Now().Add(time.Hour))

	resumeResult, err := engine.Resume(ctx, store, trk, result.ExecutionID, "7", time.Now)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !resumeResult.Resumed {
		t.Fatal("Resumed = false, want true")
	}
	if resumeResult.Issue.State != domain.StateReady {
		t.Fatalf("Issue.State = %s, want READY", resumeResult.Issue.State)
	}
	if resumeResult.Context.PreviousQuestion != "which config flag?" {
		t.Errorf("Context.PreviousQuestion = %q", resumeResult.Context.PreviousQuestion)
	}
	if len(resumeResult.Context.NewComments) != 1 || resumeResult.Context.NewComments[0].Body != "use FORGE_FOO" {
		t.Errorf("Context.NewComments = %+v, want exactly the new human comment", resumeResult.Context.NewComments)
	}
	if resumeResult.Context.Issue.ID != "7" {
		t.Errorf("Context.Issue.ID = %s, want 7", resumeResult.Context.Issue.ID)
	}

	// The persisted store reflects READY too.
	issue, err := store.GetIssue(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateReady {
		t.Fatalf("persisted state = %s, want READY", issue.State)
	}

	// The checkpoint now records the resumed context for the next Worker
	// invocation.
	checkpoint, err := store.GetNeedsInfoCheckpoint(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if checkpoint.ResumedAt == nil {
		t.Error("checkpoint.ResumedAt is nil, want set")
	}
	if checkpoint.ResumedContext == "" {
		t.Error("checkpoint.ResumedContext is empty, want the serialized resumed context")
	}

	events, err := store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var sawResumedEvent bool
	for _, e := range events {
		if e.Type == "needsinfo.resumed" {
			sawResumedEvent = true
		}
	}
	if !sawResumedEvent {
		t.Error("no needsinfo.resumed event recorded")
	}
}

// TestResume_ClockSkewAndOwnComment_DoesNotFalseTrigger is the fix for the
// bug where Resume classified "new human input" solely by
// c.CreatedAt.After(checkpoint.CreatedAt) with no marker check, comparing a
// tracker-server-clock timestamp against a locally captured one. It
// simulates the engine's local clock running behind the tracker's: even
// though forge's own posted comment's server CreatedAt lands "after" the
// skewed local checkpoint.CreatedAt, Resume must not treat it as new human
// input because it uses checkpoint.CommentPostedAt — the same tracker-server
// clock — as the baseline and excludes Forge's own marked comment. Only once
// a genuinely new, human-authored comment exists does Resume trigger.
func TestResume_ClockSkewAndOwnComment_DoesNotFalseTrigger(t *testing.T) {
	eng, store, trk, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"11": {ID: "11"},
	})
	// The engine's local clock (used for checkpoint.CreatedAt) runs an hour
	// behind the tracker's real clock (used for comment CreatedAt and, once
	// AddComment returns, checkpoint.CommentPostedAt).
	eng.Now = func() time.Time { return time.Now().Add(-time.Hour) }

	fake.ProgramResult("11", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "11", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// At this point only forge's own bot comment exists, and — under the
	// simulated skew — its server CreatedAt is "after" the local
	// checkpoint.CreatedAt. A naive baseline/author-blind comparison would
	// misclassify it as new human input.
	resumeResult, err := engine.Resume(ctx, store, trk, result.ExecutionID, "11", time.Now)
	if err != nil {
		t.Fatalf("Resume (before any human comment): %v", err)
	}
	if resumeResult.Resumed {
		t.Fatal("Resumed = true, want false: only forge's own comment exists, no human input yet")
	}

	// A genuine human reply, clearly after forge's own comment, must trigger
	// resume.
	trk.AddHumanComment("11", "alice", "use FORGE_FOO", time.Now().Add(time.Minute))

	resumeResult, err = engine.Resume(ctx, store, trk, result.ExecutionID, "11", time.Now)
	if err != nil {
		t.Fatalf("Resume (after human comment): %v", err)
	}
	if !resumeResult.Resumed {
		t.Fatal("Resumed = false, want true after a genuine human comment")
	}
	if len(resumeResult.Context.NewComments) != 1 || resumeResult.Context.NewComments[0].Author != "alice" {
		t.Errorf("Context.NewComments = %+v, want exactly alice's comment", resumeResult.Context.NewComments)
	}
}

// TestResume_SameIdentityHumanComment_TransitionsNeedsInfoToReady covers the
// dogfooding path where Forge posts tracker comments through the same account
// the human operator uses. The human reply has the same Author as Forge's own
// NEEDS_INFO comment, so Resume must distinguish Forge's comment by its body
// marker instead of by tracker identity.
func TestResume_SameIdentityHumanComment_TransitionsNeedsInfoToReady(t *testing.T) {
	eng, store, trk, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"12": {ID: "12"},
	})
	fake.ProgramResult("12", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which token should this use?"},
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "12", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	trk.AddHumanComment("12", botAuthor, "use the operator token", time.Now().Add(time.Minute))

	resumeResult, err := engine.Resume(ctx, store, trk, result.ExecutionID, "12", time.Now)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !resumeResult.Resumed {
		t.Fatal("Resumed = false, want true for same-identity human reply")
	}
	if resumeResult.Issue.State != domain.StateReady {
		t.Fatalf("Issue.State = %s, want READY", resumeResult.Issue.State)
	}
	if len(resumeResult.Context.NewComments) != 1 || resumeResult.Context.NewComments[0].Body != "use the operator token" {
		t.Errorf("Context.NewComments = %+v, want exactly the same-identity human reply", resumeResult.Context.NewComments)
	}
}

// TestResume_NoNewComment_StaysNeedsInfo asserts Resume leaves the Issue in
// NEEDS_INFO (no transition) when no comment postdates the checkpoint.
func TestResume_NoNewComment_StaysNeedsInfo(t *testing.T) {
	eng, store, trk, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"9": {ID: "9"},
	})
	fake.ProgramResult("9", agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which env?"},
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "9", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	resumeResult, err := engine.Resume(ctx, store, trk, result.ExecutionID, "9", time.Now)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumeResult.Resumed {
		t.Fatal("Resumed = true, want false (no new comment)")
	}
	if resumeResult.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("Issue.State = %s, want still NEEDS_INFO", resumeResult.Issue.State)
	}

	issue, err := store.GetIssue(ctx, result.ExecutionID, "9")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateNeedsInfo {
		t.Fatalf("persisted state = %s, want still NEEDS_INFO", issue.State)
	}
}

// TestResumeExecution_NeedsInfoResumed_PassesHumanAnswerAsFeedback is issue
// 475's fix: once Resume finds new human input and transitions an Issue
// NEEDS_INFO -> READY, ResumeExecution's re-invocation of the Agent must
// carry the human's answer (the resumed context's previous question and new
// comments) as agent.Feedback, not nil, so the Agent does not re-ask the
// same question.
func TestResumeExecution_NeedsInfoResumed_PassesHumanAnswerAsFeedback(t *testing.T) {
	eng, _, trk, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7"},
	})
	fake.ProgramResult("7", agent.AgentResult{
		Status: agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: "which config flag?",
		},
	})
	fake.ProgramDefault(agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "7", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("state = %s, want NEEDS_INFO", result.Issue.State)
	}

	trk.AddHumanComment("7", "alice", "use FORGE_FOO", time.Now().Add(time.Hour))

	// ResumeExecution alone drives the whole NEEDS_INFO -> READY -> Agent
	// re-invocation path, mirroring `forge resume`'s real call (cmd/forge
	// calls only ResumeExecution; it does not call Resume itself first).
	if _, err := eng.ResumeExecution(ctx, result.ExecutionID); err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) < 2 {
		t.Fatalf("len(invocations) = %d, want at least 2 (original + resumed)", len(invocations))
	}
	last := invocations[len(invocations)-1]
	if len(last.Feedback) == 0 {
		t.Fatal("resumed invocation's Feedback is empty, want the human's answer")
	}
	var sawQuestion, sawAnswer bool
	for _, fb := range last.Feedback {
		if strings.Contains(fb.Message, "which config flag?") {
			sawQuestion = true
		}
		if strings.Contains(fb.Message, "use FORGE_FOO") {
			sawAnswer = true
		}
	}
	if !sawQuestion {
		t.Errorf("Feedback = %+v, want a message referencing the previous question", last.Feedback)
	}
	if !sawAnswer {
		t.Errorf("Feedback = %+v, want a message containing the human's answer", last.Feedback)
	}
}

// TestResume_NoCheckpoint_ReturnsError asserts Resume errors when called
// against an Issue that never entered NEEDS_INFO.
func TestResume_NoCheckpoint_ReturnsError(t *testing.T) {
	store := openTestStore(t)
	repoRoot, base := gittest.NewTempRepo(t)
	trk := newFakeTracker()
	trk.issues["5"] = domain.Issue{ID: "5"}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := agent.NewFakeAgent()
	fake.ProgramResult("5", agent.AgentResult{Status: agent.StatusImplemented})
	eng := engine.New(store, trk, mgr, fake, config.Default(), repoRoot)

	ctx := context.Background()
	result, err := eng.Execute(ctx, "5", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := engine.Resume(ctx, store, trk, result.ExecutionID, "5", time.Now); err == nil {
		t.Fatal("Resume: want error for an Issue with no needs-info checkpoint, got nil")
	}
}
