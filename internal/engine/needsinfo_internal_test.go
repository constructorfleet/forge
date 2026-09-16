package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/executeloop"
	"github.com/Teagan42/forge/internal/steering"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// inMemoryStore is a minimal in-package storage.Store double covering only
// what handleNeedsInfo touches (checkpoints, transitions, events), so this
// white-box test can call the unexported handler directly without a real
// SQLite database.
type inMemoryStore struct {
	storage.Store // embed to satisfy Store; only overridden methods below are called

	checkpoints map[string]storage.NeedsInfoCheckpoint
	state       domain.IssueState
	events      []storage.Event
}

func (s *inMemoryStore) GetNeedsInfoCheckpoint(_ context.Context, executionID, issueID string) (storage.NeedsInfoCheckpoint, error) {
	cp, ok := s.checkpoints[executionID+"/"+issueID]
	if !ok {
		return storage.NeedsInfoCheckpoint{}, storage.ErrNotFound
	}
	return cp, nil
}

func (s *inMemoryStore) SaveNeedsInfoCheckpoint(_ context.Context, checkpoint storage.NeedsInfoCheckpoint) error {
	if s.checkpoints == nil {
		s.checkpoints = map[string]storage.NeedsInfoCheckpoint{}
	}
	s.checkpoints[checkpoint.ExecutionID+"/"+checkpoint.IssueID] = checkpoint
	return nil
}

func (s *inMemoryStore) AppendEvent(_ context.Context, event storage.Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *inMemoryStore) ReleaseWorkerClaim(context.Context, string, string) error {
	return nil
}

func (s *inMemoryStore) TransitionIssue(_ context.Context, _, _ string, to domain.IssueState) (domain.Issue, error) {
	s.state = to
	return domain.Issue{State: to}, nil
}

func (s *inMemoryStore) GetIssue(context.Context, string, string) (domain.Issue, error) {
	return domain.Issue{State: s.state}, nil
}

// countingTracker records how many times AddLabel/AddComment are called.
type countingTracker struct {
	labelCalls   int
	commentCalls int
}

func (c *countingTracker) AddLabel(_ context.Context, _ string, _ string) error {
	c.labelCalls++
	return nil
}

func (c *countingTracker) AddComment(_ context.Context, _ string, _ string) (tracker.Comment, error) {
	c.commentCalls++
	return tracker.Comment{Author: "forge-bot", CreatedAt: time.Now()}, nil
}

// TestHandleNeedsInfo_IdempotentOnRepeat calls handleNeedsInfo twice to
// completion for the same Execution/Issue (e.g. an external caller retrying
// after both calls succeeded) and asserts the label and comment are each
// posted exactly once. This does NOT simulate a crash between AddComment
// succeeding and the checkpoint write that records CommentPosted=true —
// see handleNeedsInfo's doc comment for why that narrower window is not
// fully closed.
func TestHandleNeedsInfo_IdempotentOnRepeat(t *testing.T) {
	store := &inMemoryStore{}
	trk := &countingTracker{}
	e := &Engine{
		Store:            store,
		NeedsInfoTracker: trk,
		Config:           config.Default(),
		Now:              time.Now,
	}
	result := agent.AgentResult{
		Status: agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: "which config flag?",
			Context:  "ambiguous flags in .forge.yaml",
		},
	}

	if _, err := e.handleNeedsInfo(context.Background(), "exec-1", "7", "worker-1", result); err != nil {
		t.Fatalf("first handleNeedsInfo: %v", err)
	}
	if _, err := e.handleNeedsInfo(context.Background(), "exec-1", "7", "worker-1", result); err != nil {
		t.Fatalf("second handleNeedsInfo: %v", err)
	}

	if trk.commentCalls != 1 {
		t.Errorf("commentCalls = %d, want 1 (idempotent)", trk.commentCalls)
	}
	// AddLabel is called every time (it is itself idempotent per
	// tracker.Tracker's contract), but never more than once per call.
	if trk.labelCalls != 2 {
		t.Errorf("labelCalls = %d, want 2 (called once per handleNeedsInfo call)", trk.labelCalls)
	}
	if store.state != domain.StateNeedsInfo {
		t.Errorf("final state = %s, want NEEDS_INFO", store.state)
	}
	cp := store.checkpoints["exec-1/7"]
	if !cp.CommentPosted {
		t.Error("checkpoint.CommentPosted = false, want true")
	}
}

// TestHandleNeedsInfo_NilTracker_LabelAddedReflectsNoLabelActuallyAdded
// asserts checkpoint.LabelAdded tracks whether AddLabel actually ran (label
// configured AND a NeedsInfoTracker present), not just whether a label is
// configured — a nil NeedsInfoTracker with a configured label must persist
// LabelAdded=false, since nothing was added.
func TestHandleNeedsInfo_NilTracker_LabelAddedReflectsNoLabelActuallyAdded(t *testing.T) {
	store := &inMemoryStore{}
	e := &Engine{
		Store:            store,
		NeedsInfoTracker: nil,
		Config:           config.Default(), // Blocked.Label is non-empty by default
		Now:              time.Now,
	}
	result := agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	}

	if _, err := e.handleNeedsInfo(context.Background(), "exec-2", "9", "worker-1", result); err != nil {
		t.Fatalf("handleNeedsInfo: %v", err)
	}

	cp := store.checkpoints["exec-2/9"]
	if cp.LabelAdded {
		t.Error("checkpoint.LabelAdded = true, want false (no NeedsInfoTracker to add it)")
	}
	if cp.CommentPosted {
		t.Error("checkpoint.CommentPosted = true, want false (no NeedsInfoTracker to post it)")
	}
}

// TestHandleNeedsInfo_SetsSessionStatusNeedsInfo proves TKT-005's core
// acceptance criterion at the handler level: handleNeedsInfo is the single
// choke point every StatusNeedsInfo AgentResult (real or synthetic, see
// escalateReviewToNeedsInfo) funnels through, so setting the Engine's
// executeloop.Session status here covers every caller.
func TestHandleNeedsInfo_SetsSessionStatusNeedsInfo(t *testing.T) {
	store := &inMemoryStore{}
	session := executeloop.NewSession()
	e := &Engine{
		Store:   store,
		Config:  config.Default(),
		Now:     time.Now,
		Session: session,
	}
	result := agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	}

	if _, err := e.handleNeedsInfo(context.Background(), "exec-3", "11", "worker-1", result); err != nil {
		t.Fatalf("handleNeedsInfo: %v", err)
	}

	if got := session.Status(); got != executeloop.StatusNeedsInfo {
		t.Errorf("Session.Status() = %q, want %q", got, executeloop.StatusNeedsInfo)
	}
}

// TestHandleNeedsInfo_SteeringRegistryStatusReflectsTransition proves
// TKT-009's end-to-end acceptance criterion at the Engine layer: a caller
// resolving loop-id through the same SteeringRegistry ExecuteInExecution
// registers into (steering.Registry, constructorfleet/forge#739) sees the
// status reported by that Registry change from running to needs_info once
// handleNeedsInfo runs, and back to running once the Session is restored
// (waitForSteeringAndReclaim's own transition, exercised directly here since
// this white-box test calls handleNeedsInfo without a full Execute run).
func TestHandleNeedsInfo_SteeringRegistryStatusReflectsTransition(t *testing.T) {
	store := &inMemoryStore{}
	session := executeloop.NewSession()
	registry := steering.NewRegistry()
	queue := steering.NewQueue()
	loopID := "exec-5"
	registry.Register(loopID, queue, session)
	defer registry.Unregister(loopID)

	e := &Engine{
		Store:            store,
		Config:           config.Default(),
		Now:              time.Now,
		Session:          session,
		SteeringRegistry: registry,
	}

	if got, err := registry.Status(loopID); err != nil || got != executeloop.StatusRunning {
		t.Fatalf("Status() before handleNeedsInfo = (%q, %v), want (%q, nil)", got, err, executeloop.StatusRunning)
	}

	result := agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	}
	if _, err := e.handleNeedsInfo(context.Background(), loopID, "11", "worker-1", result); err != nil {
		t.Fatalf("handleNeedsInfo: %v", err)
	}

	if got, err := registry.Status(loopID); err != nil || got != executeloop.StatusNeedsInfo {
		t.Fatalf("Status() after handleNeedsInfo = (%q, %v), want (%q, nil)", got, err, executeloop.StatusNeedsInfo)
	}

	session.SetStatus(executeloop.StatusRunning)

	if got, err := registry.Status(loopID); err != nil || got != executeloop.StatusRunning {
		t.Fatalf("Status() after resume = (%q, %v), want (%q, nil)", got, err, executeloop.StatusRunning)
	}
}

// TestHandleNeedsInfo_NilSession_DoesNotPanic asserts Session is optional,
// like NeedsInfoTracker and the other Engine collaborators wired in later
// tickets: a caller who has not wired a Session yet keeps compiling and
// running unchanged.
func TestHandleNeedsInfo_NilSession_DoesNotPanic(t *testing.T) {
	store := &inMemoryStore{}
	e := &Engine{
		Store:   store,
		Config:  config.Default(),
		Now:     time.Now,
		Session: nil,
	}
	result := agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	}

	if _, err := e.handleNeedsInfo(context.Background(), "exec-4", "12", "worker-1", result); err != nil {
		t.Fatalf("handleNeedsInfo: %v", err)
	}
}

// TestHandleNeedsInfo_CalledTwice_SessionStaysSingleScalarStatus proves
// TKT-005's "at most one outstanding NEEDS_INFO at a time" criterion: a
// second NEEDS_INFO signal for the same Session does not create a separate
// tracked request or identifier, since executeloop.Session holds a single
// scalar status field, not a list keyed by ID (see executeloop.Session).
func TestHandleNeedsInfo_CalledTwice_SessionStaysSingleScalarStatus(t *testing.T) {
	store := &inMemoryStore{}
	session := executeloop.NewSession()
	e := &Engine{
		Store:   store,
		Config:  config.Default(),
		Now:     time.Now,
		Session: session,
	}

	first := agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "which config flag?"},
	}
	if _, err := e.handleNeedsInfo(context.Background(), "exec-5", "13", "worker-1", first); err != nil {
		t.Fatalf("first handleNeedsInfo: %v", err)
	}

	second := agent.AgentResult{
		Status:    agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{Question: "a different question entirely"},
	}
	if _, err := e.handleNeedsInfo(context.Background(), "exec-5", "13", "worker-1", second); err != nil {
		t.Fatalf("second handleNeedsInfo: %v", err)
	}

	if got := session.Status(); got != executeloop.StatusNeedsInfo {
		t.Errorf("Session.Status() = %q, want %q", got, executeloop.StatusNeedsInfo)
	}
}
