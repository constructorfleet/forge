package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gate/gatetest"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/workspace"
)

// stubTracker is a minimal engine.IssueFetcher double.
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

var _ engine.IssueFetcher = (*stubTracker)(nil)

// spyWorkspaces wraps a *workspace.Manager and records whether Cleanup was
// called, so tests can assert failOut actually removes an orphaned
// Workspace rather than leaking it.
type spyWorkspaces struct {
	mgr *workspace.Manager

	mu            sync.Mutex
	cleanupCalled bool
}

func (s *spyWorkspaces) Create(ctx context.Context, executionID, issueID, base string) (domain.Workspace, error) {
	return s.mgr.Create(ctx, executionID, issueID, base)
}

func (s *spyWorkspaces) Cleanup(ctx context.Context, executionID, issueID string) error {
	s.mu.Lock()
	s.cleanupCalled = true
	s.mu.Unlock()
	return s.mgr.Cleanup(ctx, executionID, issueID)
}

func (s *spyWorkspaces) CleanupCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupCalled
}

var _ engine.WorkspaceCreator = (*spyWorkspaces)(nil)

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
	ws    *spyWorkspaces
}

func newTestEngine(t *testing.T, issues map[string]domain.Issue) testEngine {
	t.Helper()
	repoRoot, base := gittest.NewTempRepo(t)
	store := openTestStore(t)
	trk := &stubTracker{issues: issues}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	ws := &spyWorkspaces{mgr: mgr}
	fake := agent.NewFakeAgent()
	eng := engine.New(store, trk, ws, fake, config.Default(), repoRoot)
	return testEngine{eng: eng, store: store, base: base, trk: trk, fake: fake, ws: ws}
}

func TestExecute_HappyPath_NoGatesConfiguredReachesReviewing(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"42": {ID: "42"},
	})
	te.fake.ProgramResult("42", agent.AgentResult{Status: agent.StatusImplemented, Summary: "did the thing"})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "42", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// config.Default() configures no Quality Gates, so the Gate Runner has
	// nothing to run and trivially "passes" straight through to REVIEWING
	// (ticket 20's resting state).
	if result.Issue.State != domain.StateReviewing {
		t.Fatalf("final state = %s, want REVIEWING", result.Issue.State)
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
	if len(state.Issues) != 1 || state.Issues[0].State != domain.StateReviewing {
		t.Fatalf("persisted issues = %+v, want one Issue in REVIEWING", state.Issues)
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
		"issue.transitioned", // -> REVIEWING
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
	if lastTransition.From != string(domain.StateValidating) || lastTransition.To != string(domain.StateReviewing) {
		t.Errorf("last transition = %+v, want VALIDATING -> REVIEWING", lastTransition)
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

	// The happy path never orphans the Workspace, so Cleanup is not called.
	if te.ws.CleanupCalled() {
		t.Error("Cleanup was called on the happy path, want it left in place")
	}
}

// TestExecute_QualityGatesPass_AdvancesToReviewing is ticket 19's
// integration test: a fake Agent reports IMPLEMENTED, every configured
// Quality Gate passes, and the Issue advances to REVIEWING.
func TestExecute_QualityGatesPass_AdvancesToReviewing(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"20": {ID: "20"},
	})
	te.fake.ProgramResult("20", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
	}
	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramResult("make test", 0, "tests ok", "")
	runner.ProgramResult("make lint", 0, "lint ok", "")
	te.eng.Gates = runner

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "20", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateReviewing {
		t.Fatalf("final state = %s, want REVIEWING", result.Issue.State)
	}
	if calls, want := runner.Calls(), []string{"make test", "make lint"}; len(calls) != len(want) {
		t.Fatalf("got %d gate calls, want %d: %v", len(calls), len(want), calls)
	}

	runs, err := te.store.GateRunsByIssue(ctx, result.ExecutionID, "20")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d persisted gate runs, want 2: %+v", len(runs), runs)
	}
	for i, name := range []string{"test", "lint"} {
		if runs[i].Name != name || !runs[i].Passed {
			t.Errorf("runs[%d] = %+v, want Name %s and Passed true", i, runs[i], name)
		}
	}
}

// TestExecute_QualityGateFails_RoutesToFailedWithDiagnostic is ticket 19's
// other integration test: a fake Agent reports IMPLEMENTED, one configured
// Quality Gate fails, subsequent gates are skipped, and the Issue ends in
// FAILED with the diagnostic persisted — the full bounded stdout/stderr via
// the gate_runs row (Store.GateRunsByIssue), and a lean "gate.failed" Event
// (name/command/exit_code only, not a duplicate of the captured output).
// The gate retry budget is pinned to 0 so this exercises the
// budget-immediately-exhausted path of ticket 21's repair loop (a single
// gate attempt, straight to FAILED) rather than its retry path, which has
// its own dedicated tests in retry_test.go.
func TestExecute_QualityGateFails_RoutesToFailedWithDiagnostic(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"21": {ID: "21"},
	})
	te.fake.ProgramResult("21", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{
		{Name: "test", Command: "make test"},
		{Name: "lint", Command: "make lint"},
	}
	te.eng.Config.Retry.Gate = 0
	runner := gatetest.NewFakeCommandRunner()
	runner.ProgramResult("make test", 1, "1 test failed", "assertion error in foo_test.go")
	te.eng.Gates = runner

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "21", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateFailed {
		t.Fatalf("final state = %s, want FAILED", result.Issue.State)
	}
	if !result.Issue.State.IsTerminal() {
		t.Error("FAILED should be terminal")
	}

	// The second gate (lint) must not have run: first failure stops
	// subsequent gates by default.
	if calls := runner.Calls(); len(calls) != 1 {
		t.Fatalf("got %d gate calls, want 1 (lint should not have run): %v", len(calls), calls)
	}

	runs, err := te.store.GateRunsByIssue(ctx, result.ExecutionID, "21")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(runs) != 1 || runs[0].Name != "test" || runs[0].Passed {
		t.Fatalf("persisted gate runs = %+v, want one failing 'test' run", runs)
	}
	if runs[0].ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", runs[0].ExitCode)
	}
	if runs[0].Stdout != "1 test failed" || runs[0].Stderr != "assertion error in foo_test.go" {
		t.Errorf("persisted GateRun Stdout/Stderr = %q/%q, want the full captured output", runs[0].Stdout, runs[0].Stderr)
	}

	// The "gate.failed" Event stays lean — name, command, exit code — since
	// the full output already lives in the gate_runs row asserted above.
	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var gateFailed *storage.Event
	for i := range events {
		if events[i].Type == "gate.failed" {
			gateFailed = &events[i]
		}
	}
	if gateFailed == nil {
		t.Fatalf("no gate.failed event found among %+v", events)
	}
	for _, want := range []string{"test", "make test"} {
		if !strings.Contains(gateFailed.Data, want) {
			t.Errorf("gate.failed event data = %s, want it to contain %q", gateFailed.Data, want)
		}
	}
	if strings.Contains(gateFailed.Data, "1 test failed") || strings.Contains(gateFailed.Data, "assertion error") {
		t.Errorf("gate.failed event data = %s, want it to NOT duplicate the captured gate output", gateFailed.Data)
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

func TestExecute_Failed_RoutesDirectlyToFailed(t *testing.T) {
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

	// FAILED is a direct, legal edge from IMPLEMENTING (domain.state.go): no
	// VALIDATING layover, so the audit log must not claim one happened.
	events, err := te.store.EventsByExecution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatalf("EventsByExecution: %v", err)
	}
	var lastTransition struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(events[len(events)-1].Data), &lastTransition); err != nil {
		t.Fatalf("unmarshal last transition event: %v", err)
	}
	if lastTransition.From != string(domain.StateImplementing) || lastTransition.To != string(domain.StateFailed) {
		t.Errorf("last transition = %+v, want IMPLEMENTING -> FAILED", lastTransition)
	}
	for _, e := range events {
		if e.Type == "issue.transitioned" && json.Valid([]byte(e.Data)) {
			var tr struct {
				To string `json:"to"`
			}
			_ = json.Unmarshal([]byte(e.Data), &tr)
			if tr.To == string(domain.StateValidating) {
				t.Errorf("unexpected VALIDATING layover event: %+v", e)
			}
		}
	}
}

// TestExecute_AgentError_CleansUpWorkspaceAndFails drives an Agent error
// (the path a real backend failing mid-invocation would take) and asserts
// failOut ran: the orphaned Workspace is cleaned up and the Issue ends in
// the terminal FAILED state rather than stuck in IMPLEMENTING.
func TestExecute_AgentError_CleansUpWorkspaceAndFails(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"13": {ID: "13"},
	})
	te.fake.ProgramError("13", errors.New("boom: agent subprocess crashed"))

	// Execute returns a zero ExecuteResult on error, so pin the Execution ID
	// it will mint to inspect the persisted Issue afterward.
	const executionID = "exec-agent-error"
	te.eng.NewExecutionID = func() string { return executionID }

	ctx := context.Background()
	_, err := te.eng.Execute(ctx, "13", te.base)
	if err == nil {
		t.Fatal("Execute: want error when the Agent errors, got nil")
	}

	if !te.ws.CleanupCalled() {
		t.Error("Cleanup was not called after an Agent error, want the orphaned Workspace removed")
	}

	issue, getErr := te.store.GetIssue(ctx, executionID, "13")
	if getErr != nil {
		t.Fatalf("GetIssue: %v", getErr)
	}
	if issue.State != domain.StateFailed {
		t.Fatalf("issue.State = %s, want FAILED", issue.State)
	}
}

func TestExecute_UnknownTrackerIssue_ReturnsError(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{})
	if _, err := te.eng.Execute(context.Background(), "missing", te.base); err == nil {
		t.Fatal("Execute: want error for unknown issue, got nil")
	}
}

func TestLoadStatus_ReflectsPersistedStateAfterExecute(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"11": {ID: "11"},
	})
	te.fake.ProgramResult("11", agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "11", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	report, err := engine.LoadStatus(ctx, te.store, result.ExecutionID)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if report.Execution.ID != result.ExecutionID {
		t.Errorf("report.Execution.ID = %s, want %s", report.Execution.ID, result.ExecutionID)
	}
	if len(report.Issues) != 1 || report.Issues[0].State != domain.StateReviewing {
		t.Fatalf("report.Issues = %+v, want one Issue in REVIEWING", report.Issues)
	}
	if len(report.Events) == 0 {
		t.Error("report.Events is empty, want the full transition/event log")
	}
}

func TestNew_DefaultsAreUsable(t *testing.T) {
	repoRoot, _ := gittest.NewTempRepo(t)
	store := openTestStore(t)
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	eng := engine.New(store, &stubTracker{}, &spyWorkspaces{mgr: mgr}, agent.NewFakeAgent(), config.Default(), repoRoot)

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
