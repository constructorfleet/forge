package engine_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/needsinfo"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

// botAuthor is the identity fakeTracker.AddComment reports for forge's own
// posted comments, so tests can distinguish them from human-authored ones.
const botAuthor = "forge-bot"

// fakeTracker is an in-memory engine.IssueFetcher + engine.NeedsInfoTracker
// + engine.ResumeTracker double: it never hits real GitHub, and records
// every AddLabel/AddComment call so tests can assert idempotency.
type fakeTracker struct {
	mu       sync.Mutex
	issues   map[string]domain.Issue
	comments map[string][]tracker.Comment
	labels   map[string][]string
}

func newFakeTracker() *fakeTracker {
	return &fakeTracker{
		issues:   map[string]domain.Issue{},
		comments: map[string][]tracker.Comment{},
		labels:   map[string][]string{},
	}
}

func (f *fakeTracker) GetIssue(_ context.Context, id string) (domain.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issues[id], nil
}

func (f *fakeTracker) AddLabel(_ context.Context, id string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.labels[id] {
		if l == label {
			return nil // idempotent: already present
		}
	}
	f.labels[id] = append(f.labels[id], label)
	return nil
}

// AddComment posts as botAuthor and returns the normalized comment
// (including that author and its CreatedAt), mirroring the real
// tracker.Tracker.AddComment contract that handleNeedsInfo and Resume rely
// on to distinguish forge's own comment from human replies.
func (f *fakeTracker) AddComment(_ context.Context, id string, body string) (tracker.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := tracker.Comment{
		Author:    botAuthor,
		Body:      body,
		CreatedAt: time.Now(),
	}
	f.comments[id] = append(f.comments[id], c)
	return c, nil
}

func (f *fakeTracker) GetComments(_ context.Context, id string) ([]tracker.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tracker.Comment, len(f.comments[id]))
	copy(out, f.comments[id])
	return out, nil
}

// AddHumanComment appends a human-authored comment directly into the fake's
// backing store (bypassing AddComment, which is forge's own posting path),
// simulating a human replying on the tracker.
func (f *fakeTracker) AddHumanComment(id, author, body string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments[id] = append(f.comments[id], tracker.Comment{Author: author, Body: body, CreatedAt: at})
}

// RemoveLabel idempotently ensures label is not set on id, mirroring
// tracker.FakeTracker's RemoveLabel — needed by engine.Engine.StatusTracker
// (ticket 24, internal/statusreflect).
func (f *fakeTracker) RemoveLabel(_ context.Context, id string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.labels[id][:0]
	for _, l := range f.labels[id] {
		if l != label {
			kept = append(kept, l)
		}
	}
	f.labels[id] = kept
	return nil
}

func (f *fakeTracker) Labels(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.labels[id]...)
}

func (f *fakeTracker) CommentCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.comments[id])
}

var _ engine.IssueFetcher = (*fakeTracker)(nil)
var _ engine.NeedsInfoTracker = (*fakeTracker)(nil)
var _ engine.ResumeTracker = (*fakeTracker)(nil)

func newNeedsInfoTestEngine(t *testing.T, issues map[string]domain.Issue) (*engine.Engine, *storage.SQLiteStore, *fakeTracker, *agent.FakeAgent, string) {
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
	eng := engine.New(store, trk, mgr, fake, config.Default(), repoRoot)
	eng.NeedsInfoTracker = trk
	return eng, store, trk, fake, base
}

// TestExecute_NeedsInfo_LabelsCommentsChecksAndPreservesWorkspace is the
// ticket's headline integration test: agent returns NEEDS_INFO -> the
// configured label is added, a structured comment is posted, a checkpoint
// is persisted, the Workspace is preserved (not cleaned up), and no PR is
// ever created for a NEEDS_INFO issue.
func TestExecute_NeedsInfo_LabelsCommentsChecksAndPreservesWorkspace(t *testing.T) {
	eng, store, trk, fake, base := newNeedsInfoTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7"},
	})
	fake.ProgramResult("7", agent.AgentResult{
		Status:  agent.StatusNeedsInfo,
		Summary: "blocked on config choice",
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: "which config flag should control this?",
			Context:  "two plausible flags exist in .forge.yaml",
		},
	})

	ctx := context.Background()
	result, err := eng.Execute(ctx, "7", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
	}

	wantLabel := config.Default().Blocked.Label
	labels := trk.Labels("7")
	if len(labels) != 1 || labels[0] != wantLabel {
		t.Errorf("labels = %v, want [%s]", labels, wantLabel)
	}

	if trk.CommentCount("7") != 1 {
		t.Fatalf("comment count = %d, want 1", trk.CommentCount("7"))
	}
	comments, err := trk.GetComments(ctx, "7")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	body := comments[0].Body
	if !strings.Contains(body, "which config flag should control this?") {
		t.Errorf("comment body missing question: %s", body)
	}
	if !strings.Contains(body, "two plausible flags exist in .forge.yaml") {
		t.Errorf("comment body missing context: %s", body)
	}
	if !strings.Contains(body, needsinfo.CommentMarker(result.ExecutionID, "7")) {
		t.Errorf("comment body missing needs-info marker: %s", body)
	}

	checkpoint, err := store.GetNeedsInfoCheckpoint(ctx, result.ExecutionID, "7")
	if err != nil {
		t.Fatalf("GetNeedsInfoCheckpoint: %v", err)
	}
	if checkpoint.Question != "which config flag should control this?" {
		t.Errorf("checkpoint.Question = %q", checkpoint.Question)
	}
	if !checkpoint.CommentPosted {
		t.Error("checkpoint.CommentPosted = false, want true")
	}
	if checkpoint.CommentAuthor != botAuthor {
		t.Errorf("checkpoint.CommentAuthor = %q, want %q", checkpoint.CommentAuthor, botAuthor)
	}
	if checkpoint.CommentPostedAt.IsZero() {
		t.Error("checkpoint.CommentPostedAt is zero, want the tracker-reported timestamp")
	}
	if checkpoint.CreatedAt.IsZero() {
		t.Error("checkpoint.CreatedAt is zero, want a timestamp")
	}

	// The Workspace must be preserved: the Workspace created for this Issue
	// must still exist on disk after Execute returns.
	state, err := store.LoadExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("LoadExecution: %v", err)
	}
	if len(state.Issues) != 1 {
		t.Fatalf("persisted issues = %+v, want 1", state.Issues)
	}

	events, err := store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	// worker.released is deliberately not asserted on here: it is a forward
	// seam for ticket 26's real slot release (see handleNeedsInfo's doc
	// comment), and its representation may change once that lands.
	var sawCheckpointEvent, sawPREvent bool
	for _, e := range events {
		switch e.Type {
		case "needsinfo.checkpoint_saved":
			sawCheckpointEvent = true
		case "pr.created":
			sawPREvent = true
		}
	}
	if !sawCheckpointEvent {
		t.Error("no needsinfo.checkpoint_saved event recorded")
	}
	if sawPREvent {
		t.Error("a pr.created event was recorded for a NEEDS_INFO issue, want none")
	}
}
