package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

// stubTracker is a minimal tracker.Tracker double: only GetIssue is
// exercised by Engine, so every other method is unimplemented (and would
// fail a test that unexpectedly reached it).
type stubTracker struct {
	issues map[string]domain.Issue
	err    error
}

func (s *stubTracker) GetIssue(_ context.Context, id string) (domain.Issue, error) {
	if s.err != nil {
		return domain.Issue{}, s.err
	}
	issue, ok := s.issues[id]
	if !ok {
		return domain.Issue{}, errors.New("stubTracker: no issue " + id)
	}
	return issue, nil
}

func (s *stubTracker) GetIssues(context.Context, []string) ([]domain.Issue, error) {
	panic("not implemented")
}
func (s *stubTracker) GetComments(context.Context, string) ([]tracker.Comment, error) {
	panic("not implemented")
}
func (s *stubTracker) AddComment(context.Context, string, string) error {
	panic("not implemented")
}
func (s *stubTracker) AddLabel(context.Context, string, string) error {
	panic("not implemented")
}
func (s *stubTracker) RemoveLabel(context.Context, string, string) error {
	panic("not implemented")
}
func (s *stubTracker) GetMergeRequirements(context.Context, string) (tracker.MergeRequirements, error) {
	panic("not implemented")
}

var _ tracker.Tracker = (*stubTracker)(nil)

// runGit runs a git command against dir and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// newTempRepo creates a temporary Git repository with one commit on its
// default branch and returns its root path and the commit SHA.
func newTempRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-q", "-m", "initial")
	sha := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	return root, sha
}

func openTestStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// testEngine bundles the Engine under test with the pieces a test needs to
// assert against directly (the store, for reloading persisted state, and
// the base SHA the temp repo started from).
type testEngine struct {
	eng   *engine.Engine
	store *storage.SQLiteStore
	base  string
	trk   *stubTracker
	fake  *agent.FakeAgent
}

func newTestEngine(t *testing.T, issues map[string]domain.Issue) testEngine {
	t.Helper()
	repoRoot, base := newTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: issues}
	wsMgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fake := agent.NewFakeAgent()
	eng := engine.New(store, trk, wsMgr, fake, config.Default(), repoRoot)
	return testEngine{eng: eng, store: store, base: base, trk: trk, fake: fake}
}

func TestExecute_HappyPath_ReachesValidatingWithPersistedTransitions(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	te.fake.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateValidating {
		t.Fatalf("final state = %s, want VALIDATING", result.Issue.State)
	}

	// Full state is inspectable via the persisted store afterward (the
	// same round trip `forge status` uses).
	state, err := te.store.LoadExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("LoadExecution: %v", err)
	}
	if state.Execution.BaseRevision != te.base {
		t.Errorf("Execution.BaseRevision = %s, want %s", state.Execution.BaseRevision, te.base)
	}
	if len(state.Issues) != 1 || state.Issues[0].State != domain.StateValidating {
		t.Fatalf("persisted issues = %+v, want one Issue in VALIDATING", state.Issues)
	}

	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	wantSequence := []string{
		"issue.transitioned", // -> READY
		"worker.base_captured",
		"issue.claimed",
		"issue.transitioned", // -> CLAIMED
		"issue.transitioned", // -> PREPARING
		"workspace.created",
		"issue.transitioned", // -> IMPLEMENTING
		"agent.result",
		"issue.transitioned", // -> VALIDATING
	}
	if len(events) != len(wantSequence) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(wantSequence), events)
	}
	for i, want := range wantSequence {
		if events[i].Type != want {
			t.Errorf("event %d Type = %s, want %s", i, events[i].Type, want)
		}
	}

	// The transition Events specifically carry the from/to pair, so the
	// audit log is inspectable without replaying state.
	var lastTransition struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(events[len(events)-1].Data), &lastTransition); err != nil {
		t.Fatalf("unmarshal last transition event: %v", err)
	}
	if lastTransition.From != string(domain.StateImplementing) || lastTransition.To != string(domain.StateValidating) {
		t.Errorf("last transition = %+v, want IMPLEMENTING -> VALIDATING", lastTransition)
	}

	// The Agent was invoked with the compiled Repository Context and the
	// Workspace path, i.e. the assembled Execution Context.
	invocations := te.fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("got %d agent invocations, want 1", len(invocations))
	}
	inv := invocations[0]
	if inv.Repository.BaseRevision != te.base {
		t.Errorf("Repository.BaseRevision = %s, want %s", inv.Repository.BaseRevision, te.base)
	}
	if inv.WorkspacePath == "" {
		t.Error("WorkspacePath is empty")
	}
	if info, err := os.Stat(inv.WorkspacePath); err != nil || !info.IsDir() {
		t.Errorf("WorkspacePath %s not created before agent invocation: %v", inv.WorkspacePath, err)
	}
}

func TestExecute_NeedsInfo_RoutesToNeedsInfoState(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"7": {ID: "7"},
	})
	te.fake.ProgramResult("7", agent.AgentResult{
		Status: agent.StatusNeedsInfo,
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: "which config flag?",
		},
	})

	result, err := te.eng.Execute(context.Background(), "7", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateNeedsInfo {
		t.Fatalf("final state = %s, want NEEDS_INFO", result.Issue.State)
	}
}

func TestExecute_Failed_RoutesToFailedViaValidating(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"9": {ID: "9"},
	})
	te.fake.ProgramResult("9", agent.AgentResult{Status: agent.StatusFailed, Summary: "could not proceed"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "9", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if !result.Issue.State.IsTerminal() {
		t.Error("FAILED should be terminal")
	}
}

func TestExecute_UnknownTrackerIssue_ReturnsError(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{})
	if _, err := te.eng.Execute(context.Background(), "missing", te.base); err == nil {
		t.Fatal("Execute: want error for unknown issue, got nil")
	}
}

func TestStatus_ReflectsPersistedStateAfterExecute(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"11": {ID: "11"},
	})
	te.fake.ProgramResult("11", agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "11", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	report, err := te.eng.Status(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if report.Execution.ID != result.ExecutionID {
		t.Errorf("report.Execution.ID = %s, want %s", report.Execution.ID, result.ExecutionID)
	}
	if len(report.Issues) != 1 || report.Issues[0].State != domain.StateValidating {
		t.Fatalf("report.Issues = %+v, want one Issue in VALIDATING", report.Issues)
	}
	if len(report.Events) == 0 {
		t.Error("report.Events is empty, want the full transition/event log")
	}
}

func TestNew_DefaultsAreUsable(t *testing.T) {
	repoRoot, _ := newTempRepo(t)
	store := openTestStore(t)
	wsMgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	eng := engine.New(store, &stubTracker{}, wsMgr, agent.NewFakeAgent(), config.Default(), repoRoot)

	if eng.Now == nil {
		t.Fatal("Now is nil, want a default time source")
	}
	if got := eng.Now(); time.Since(got) > time.Minute {
		t.Errorf("Now() = %v, want close to time.Now()", got)
	}
	if eng.NewExecutionID == nil {
		t.Fatal("NewExecutionID is nil, want a default ID generator")
	}
	if id := eng.NewExecutionID(); id == "" {
		t.Error("NewExecutionID() returned empty string")
	}
}
