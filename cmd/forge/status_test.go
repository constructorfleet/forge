package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
)

func TestRunExecute_RejectsWrongArgCount(t *testing.T) {
	for _, args := range [][]string{{}, {"1", "2"}} {
		if code := runExecute(args); code != 2 {
			t.Errorf("runExecute(%v) = %d, want 2 (usage error)", args, code)
		}
	}
}

func TestRunStatus_RejectsWrongArgCount(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}} {
		if code := runStatus(args); code != 2 {
			t.Errorf("runStatus(%v) = %d, want 2 (usage error)", args, code)
		}
	}
}

func TestRunStatus_ReturnsErrorForUnknownExecution(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "forge.db")
	code := runStatus([]string{"--db", dbPath, "does-not-exist"})
	if code != 1 {
		t.Errorf("runStatus for unknown execution = %d, want 1", code)
	}
}

func TestPrintStatus_IncludesExecutionIssuesAndEvents(t *testing.T) {
	report := engine.StatusReport{
		Execution: domain.Execution{ID: "exec-1", BaseRevision: "deadbeef", StartedAt: time.Unix(0, 0).UTC()},
		Issues: []domain.Issue{
			{ID: "42", State: domain.StateValidating, Scope: domain.ScopeManaged},
		},
		Events: []storage.Event{
			{IssueID: "42", Type: "issue.transitioned", Data: `{"from":"IMPLEMENTING","to":"VALIDATING"}`, OccurredAt: time.Unix(0, 0).UTC()},
		},
	}

	var buf bytes.Buffer
	printStatus(&buf, report)
	out := buf.String()

	for _, want := range []string{"exec-1", "deadbeef", "42", string(domain.StateValidating), "issue.transitioned"} {
		if !strings.Contains(out, want) {
			t.Errorf("printStatus output missing %q:\n%s", want, out)
		}
	}
}

// TestRunStatus_ReflectsExecuteOutput drives Engine directly (as
// buildEngine would, minus the GitHub/git specifics runExecute's own flags
// resolve) to populate a real SQLite database, then exercises runStatus
// against it end-to-end.
func TestRunStatus_ReflectsExecuteOutput(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "forge.db")
	ctx := context.Background()
	store, err := openStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}

	root, base := newTempRepo(t)
	wsMgr := mustWorkspaceManager(t, root)
	trk := &stubTrackerForCLI{issue: domain.Issue{ID: "5"}}
	fake := newProgrammedFakeAgent(t, "5")

	eng := engine.New(store, trk, wsMgr, fake, mustConfig(), root)
	result, err := eng.Execute(ctx, "5", base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	code := runStatus([]string{"--db", dbPath, result.ExecutionID})
	if code != 0 {
		t.Fatalf("runStatus = %d, want 0", code)
	}
}
