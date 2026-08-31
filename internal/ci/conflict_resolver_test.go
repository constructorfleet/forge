package ci_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/gate/gatetest"
)

type stubConflictCandidates struct {
	candidate ci.ConflictCandidate
	conflicts []string
	err       error

	createCalls       int
	createOriginalSHA []string
	rebaseCalls       int
	rebasePath        []string
	rebaseBase        []string
	headCalls         int
	cleanupCalls      int
	cleanupPath       []string
}

func (s *stubConflictCandidates) CreateConflictCandidate(_ context.Context, _, _ string, originalSHA string) (ci.ConflictCandidate, error) {
	s.createCalls++
	s.createOriginalSHA = append(s.createOriginalSHA, originalSHA)
	return s.candidate, s.err
}

func (s *stubConflictCandidates) RebaseConflictCandidate(_ context.Context, candidate ci.ConflictCandidate, base string) ([]string, error) {
	s.rebaseCalls++
	s.rebasePath = append(s.rebasePath, candidate.Path)
	s.rebaseBase = append(s.rebaseBase, base)
	return s.conflicts, s.err
}

func (s *stubConflictCandidates) ConflictCandidateHead(_ context.Context, candidate ci.ConflictCandidate) (string, error) {
	s.headCalls++
	return candidate.HeadSHA, nil
}

func (s *stubConflictCandidates) CleanupConflictCandidate(_ context.Context, candidate ci.ConflictCandidate) error {
	s.cleanupCalls++
	s.cleanupPath = append(s.cleanupPath, candidate.Path)
	return nil
}

type stubConflictBranchPusher struct {
	calls       int
	paths       []string
	branch      []string
	expectedSHA []string
	err         error
	readyErr    error
}

func (s *stubConflictBranchPusher) EnsureWorkspaceReady(context.Context, string) error {
	return s.readyErr
}

func (s *stubConflictBranchPusher) ForcePushWithLease(_ context.Context, path, branch, expectedSHA string) error {
	s.calls++
	s.paths = append(s.paths, path)
	s.branch = append(s.branch, branch)
	s.expectedSHA = append(s.expectedSHA, expectedSHA)
	return s.err
}

func TestWorkspaceConflictResolver_GateFailureRefusesWithoutPushing(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-gate", "34")
	seedWorkspace(t, store, "exec-conflict-gate", "34", "/tmp/ws-34", "forge/exec-conflict-gate/34")

	cfg := config.Default()
	cfg.Quality.Gates = []config.QualityGate{{Name: "test", Command: "go test ./..."}}
	gates := gatetest.NewFakeCommandRunner()
	gates.ProgramResult("go test ./...", 1, "", "compile failed")

	candidates := &stubConflictCandidates{
		candidate: ci.ConflictCandidate{Path: "/tmp/candidate-34", Branch: "forge/conflict-resolution/exec-conflict-gate/34", HeadSHA: "def456"},
	}
	pusher := &stubConflictBranchPusher{}
	resolver := ci.NewWorkspaceConflictResolver(store, candidates, pusher, nil, gates, cfg)

	result, err := resolver.ResolveMergeConflict(context.Background(), ci.ConflictResolutionRequest{
		ExecutionID:        "exec-conflict-gate",
		IssueID:            "34",
		BaseBranch:         "main",
		PullRequestHeadSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("ResolveMergeConflict: %v", err)
	}
	if result.Resolved {
		t.Fatal("Resolved = true, want false after quality gate failure")
	}
	if !strings.Contains(result.Details, "quality gate test failed") {
		t.Fatalf("Details = %q, want quality gate failure reason", result.Details)
	}
	if candidates.createCalls != 1 || candidates.createOriginalSHA[0] != "abc123" {
		t.Fatalf("CreateCandidate calls/original = %d/%v, want abc123", candidates.createCalls, candidates.createOriginalSHA)
	}
	if candidates.rebaseCalls != 1 || candidates.rebasePath[0] != "/tmp/candidate-34" {
		t.Fatalf("RebaseCandidate calls/path = %d/%v, want /tmp/candidate-34", candidates.rebaseCalls, candidates.rebasePath)
	}
	if pusher.calls != 0 {
		t.Fatalf("ForcePushWithLease calls = %d, want 0 after gate failure", pusher.calls)
	}
	if candidates.cleanupCalls != 1 || candidates.cleanupPath[0] != "/tmp/candidate-34" {
		t.Fatalf("CleanupCandidate calls/path = %d/%v, want /tmp/candidate-34", candidates.cleanupCalls, candidates.cleanupPath)
	}

	gateRuns, err := store.GateRunsByIssue(context.Background(), "exec-conflict-gate", "34")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(gateRuns) != 1 || gateRuns[0].Name != "test" || gateRuns[0].Passed {
		t.Fatalf("gate runs = %+v, want one failed test gate", gateRuns)
	}
}

func TestWorkspaceConflictResolver_RebaseAndPassingGatesPushesCandidateWithRecordedHeadLease(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-push", "35")
	seedWorkspace(t, store, "exec-conflict-push", "35", "/tmp/ws-35", "forge/exec-conflict-push/35")

	cfg := config.Default()
	cfg.Quality.Gates = []config.QualityGate{
		{Name: "test", Command: "go test ./..."},
		{Name: "vet", Command: "go vet ./..."},
	}
	gates := gatetest.NewFakeCommandRunner()

	candidates := &stubConflictCandidates{
		candidate: ci.ConflictCandidate{Path: "/tmp/candidate-35", Branch: "forge/conflict-resolution/exec-conflict-push/35", HeadSHA: "def456"},
	}
	pusher := &stubConflictBranchPusher{}
	resolver := ci.NewWorkspaceConflictResolver(store, candidates, pusher, nil, gates, cfg)

	result, err := resolver.ResolveMergeConflict(context.Background(), ci.ConflictResolutionRequest{
		ExecutionID:        "exec-conflict-push",
		IssueID:            "35",
		PullRequestNumber:  23,
		BaseBranch:         "main",
		PullRequestHeadSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("ResolveMergeConflict: %v", err)
	}
	if !result.Resolved {
		t.Fatalf("Resolved = false, want true; details: %s", result.Details)
	}
	if candidates.createCalls != 1 || candidates.createOriginalSHA[0] != "abc123" {
		t.Fatalf("CreateCandidate calls/original = %d/%v, want abc123", candidates.createCalls, candidates.createOriginalSHA)
	}
	if candidates.rebaseCalls != 1 || candidates.rebaseBase[0] != "main" {
		t.Fatalf("RebaseCandidate calls/newBases = %d/%v, want one rebase onto main", candidates.rebaseCalls, candidates.rebaseBase)
	}
	if got := gates.Calls(); len(got) != 2 || got[0] != "go test ./..." || got[1] != "go vet ./..." {
		t.Fatalf("gate calls = %v, want test then vet", got)
	}
	if got := gates.WorkDirs(); len(got) != 2 || got[0] != "/tmp/candidate-35" || got[1] != "/tmp/candidate-35" {
		t.Fatalf("gate work dirs = %v, want candidate workspace", got)
	}
	if pusher.calls != 1 {
		t.Fatalf("ForcePushWithLease calls = %d, want 1", pusher.calls)
	}
	if pusher.paths[0] != "/tmp/candidate-35" || pusher.branch[0] != "forge/exec-conflict-push/35" || pusher.expectedSHA[0] != "abc123" {
		t.Fatalf("ForcePushWithLease path/branch/expected = %q/%q/%q, want candidate/live branch/abc123", pusher.paths[0], pusher.branch[0], pusher.expectedSHA[0])
	}
	if candidates.cleanupCalls != 1 {
		t.Fatalf("CleanupCandidate calls = %d, want 1", candidates.cleanupCalls)
	}

	gateRuns, err := store.GateRunsByIssue(context.Background(), "exec-conflict-push", "35")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(gateRuns) != 2 || !gateRuns[0].Passed || !gateRuns[1].Passed {
		t.Fatalf("gate runs = %+v, want two passing gates", gateRuns)
	}

	attempt, err := store.ActiveConflictResolutionAttempt(context.Background(), "exec-conflict-push", "35")
	if err != nil {
		t.Fatalf("ActiveConflictResolutionAttempt: %v", err)
	}
	if attempt.OriginalSHA != "abc123" || attempt.CandidateSHA != "def456" || attempt.Branch != "forge/exec-conflict-push/35" || attempt.PRNumber != 23 {
		t.Fatalf("attempt = %+v, want original abc123 candidate def456 branch forge/exec-conflict-push/35 PR 23", attempt)
	}
}
