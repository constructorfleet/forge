package ci_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/gate/gatetest"
)

func TestWorkspaceConflictResolver_GateFailureRefusesWithoutPushing(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-gate", "34")
	seedWorkspace(t, store, "exec-conflict-gate", "34", "/tmp/ws-34", "forge/exec-conflict-gate/34")

	cfg := config.Default()
	cfg.Quality.Gates = []config.QualityGate{{Name: "test", Command: "go test ./..."}}
	gates := gatetest.NewFakeCommandRunner()
	gates.ProgramResult("go test ./...", 1, "", "compile failed")

	rebaser := &stubRebaser{}
	pusher := &stubBranchPusher{}
	resolver := ci.NewWorkspaceConflictResolver(store, rebaser, pusher, pusher, gates, cfg)

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
	if rebaser.calls != 1 {
		t.Fatalf("Rebase calls = %d, want 1", rebaser.calls)
	}
	if pusher.calls != 0 {
		t.Fatalf("ForcePush calls = %d, want 0 after gate failure", pusher.calls)
	}
	if len(pusher.resetSHA) != 1 || pusher.resetPath[0] != "/tmp/ws-34" || pusher.resetSHA[0] != "abc123" {
		t.Fatalf("Reset calls = %v/%v, want /tmp/ws-34 to abc123 after gate failure", pusher.resetPath, pusher.resetSHA)
	}

	gateRuns, err := store.GateRunsByIssue(context.Background(), "exec-conflict-gate", "34")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(gateRuns) != 1 || gateRuns[0].Name != "test" || gateRuns[0].Passed {
		t.Fatalf("gate runs = %+v, want one failed test gate", gateRuns)
	}
}

func TestWorkspaceConflictResolver_RebaseAndPassingGatesPushesBranch(t *testing.T) {
	store := openTestStore(t)
	seedIssueWithPR(t, store, "exec-conflict-push", "35")
	seedWorkspace(t, store, "exec-conflict-push", "35", "/tmp/ws-35", "forge/exec-conflict-push/35")

	cfg := config.Default()
	cfg.Quality.Gates = []config.QualityGate{
		{Name: "test", Command: "go test ./..."},
		{Name: "vet", Command: "go vet ./..."},
	}
	gates := gatetest.NewFakeCommandRunner()

	rebaser := &stubRebaser{}
	pusher := &stubBranchPusher{}
	resolver := ci.NewWorkspaceConflictResolver(store, rebaser, pusher, pusher, gates, cfg)

	result, err := resolver.ResolveMergeConflict(context.Background(), ci.ConflictResolutionRequest{
		ExecutionID: "exec-conflict-push",
		IssueID:     "35",
		BaseBranch:  "main",
	})
	if err != nil {
		t.Fatalf("ResolveMergeConflict: %v", err)
	}
	if !result.Resolved {
		t.Fatalf("Resolved = false, want true; details: %s", result.Details)
	}
	if rebaser.calls != 1 || rebaser.newBases[0] != "main" {
		t.Fatalf("Rebase calls/newBases = %d/%v, want one rebase onto main", rebaser.calls, rebaser.newBases)
	}
	if got := gates.Calls(); len(got) != 2 || got[0] != "go test ./..." || got[1] != "go vet ./..." {
		t.Fatalf("gate calls = %v, want test then vet", got)
	}
	if pusher.calls != 1 {
		t.Fatalf("ForcePush calls = %d, want 1", pusher.calls)
	}
	if len(pusher.resetSHA) != 0 {
		t.Fatalf("Reset calls = %v/%v, want none on success", pusher.resetPath, pusher.resetSHA)
	}
	if pusher.paths[0] != "/tmp/ws-35" || pusher.branch[0] != "forge/exec-conflict-push/35" {
		t.Fatalf("ForcePush path/branch = %q/%q, want /tmp/ws-35/forge/exec-conflict-push/35", pusher.paths[0], pusher.branch[0])
	}

	gateRuns, err := store.GateRunsByIssue(context.Background(), "exec-conflict-push", "35")
	if err != nil {
		t.Fatalf("GateRunsByIssue: %v", err)
	}
	if len(gateRuns) != 2 || !gateRuns[0].Passed || !gateRuns[1].Passed {
		t.Fatalf("gate runs = %+v, want two passing gates", gateRuns)
	}
}
