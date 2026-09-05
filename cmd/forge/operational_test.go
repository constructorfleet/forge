package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
)

func TestReportRetryError_TreatsAlreadyClaimedAsNoOp(t *testing.T) {
	var out, errOut bytes.Buffer
	err := fmt.Errorf("engine: retry issue 9: %w", engine.ErrRetryAlreadyClaimed)

	if code := reportRetryError(&out, &errOut, "exec-1", "9", err); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "already claimed") {
		t.Fatalf("stdout = %q, want it to name the claimed retry", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing on a no-op", errOut.String())
	}
}

func TestReportRetryError_TreatsStartDeferredAsNoOp(t *testing.T) {
	var out, errOut bytes.Buffer
	err := &engine.RetryStartDeferredError{Err: errors.New("tracker unreachable")}

	if code := reportRetryError(&out, &errOut, "exec-1", "9", err); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "deferred to the scheduler") {
		t.Fatalf("stdout = %q, want it to name the deferred start", out.String())
	}
	if !strings.Contains(out.String(), "tracker unreachable") {
		t.Fatalf("stdout = %q, want it to include the underlying error", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing on a no-op", errOut.String())
	}
}

func TestReportRetryError_TreatsStuckMidResumeAsFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	err := &engine.RetryResumeStuckError{Err: errors.New("workspace unreachable"), State: domain.StatePreparing}

	if code := reportRetryError(&out, &errOut, "exec-1", "9", err); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "stuck mid-resume") {
		t.Fatalf("stderr = %q, want it to name the stuck resume", errOut.String())
	}
	if !strings.Contains(errOut.String(), "forge resume exec-1") {
		t.Fatalf("stderr = %q, want it to name the forge resume command", errOut.String())
	}
	if !strings.Contains(errOut.String(), "workspace unreachable") {
		t.Fatalf("stderr = %q, want it to include the underlying error", errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing on a failure", out.String())
	}
}

func TestReportRetryError_KeepsFailureExitCode(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := reportRetryError(&out, &errOut, "exec-1", "9", errors.New("disk on fire")); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "disk on fire") {
		t.Fatalf("stderr = %q, want the original error", errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing on a failure", out.String())
	}
}

func TestRunCancel_RejectsWrongArgCount(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}} {
		if code := runCancel(args); code != 2 {
			t.Fatalf("runCancel(%v) = %d, want 2", args, code)
		}
	}
}

func TestRunRetry_RejectsWrongArgCountOrMalformedID(t *testing.T) {
	if code := runRetry(nil); code != 2 {
		t.Fatalf("runRetry(nil) = %d, want 2", code)
	}
	if code := runRetry([]string{"missing-separator"}); code != 2 {
		t.Fatalf("runRetry(malformed) = %d, want 2", code)
	}
}

func TestParseIssueExecutionID(t *testing.T) {
	executionID, issueID, err := parseIssueExecutionID("exec-123/issue-9")
	if err != nil {
		t.Fatalf("parseIssueExecutionID: %v", err)
	}
	if executionID != "exec-123" || issueID != "issue-9" {
		t.Fatalf("got (%q, %q), want (%q, %q)", executionID, issueID, "exec-123", "issue-9")
	}
}

// TestRunCancel_FailsLoudlyOutsideGitRepo covers issue #576: `forge cancel`
// must discover the repo root via git (as `forge retry` already does for
// issue #459) instead of silently treating an arbitrary cwd as the repo
// root.
func TestRunCancel_FailsLoudlyOutsideGitRepo(t *testing.T) {
	chdir(t, t.TempDir())

	if code := runCancel([]string{"exec-1"}); code != 1 {
		t.Fatalf("runCancel outside a git repo = %d, want 1", code)
	}
}

// TestRunCancel_ResolvesPathsFromSubdirectory covers issue #576: running
// `forge cancel` from a subdirectory must use the repo root's .forge.yaml
// and .forge/forge.db, not create fresh, empty ones under the subdirectory
// (the same #459 bug class `forge retry` was already fixed for).
func TestRunCancel_ResolvesPathsFromSubdirectory(t *testing.T) {
	repoRoot, base := newTempRepo(t)

	dbPath := filepath.Join(repoRoot, ".forge", "forge.db")
	store, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	exec := domain.Execution{ID: "exec-cancel-cli", BaseRevision: base, StartedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	if err := store.CreateExecution(context.Background(), exec); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if err := store.CreateIssue(context.Background(), domain.Issue{
		ID:          "99",
		ExecutionID: exec.ID,
		Title:       "Cancel from CLI",
		State:       domain.StateReady,
		Scope:       domain.ScopeManaged,
		RetryBudget: domain.NewRetryBudget(config.Default().Retry),
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sub := filepath.Join(repoRoot, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	chdir(t, sub)

	if code := runCancel([]string{exec.ID}); code != 0 {
		t.Fatalf("runCancel = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(sub, ".forge")); !os.IsNotExist(err) {
		t.Fatalf("expected no .forge directory under %s, stat err = %v", sub, err)
	}

	reopened, err := openStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	issue, err := reopened.GetIssue(context.Background(), exec.ID, "99")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.State != domain.StateCancelled {
		t.Fatalf("persisted state = %s, want CANCELLED", issue.State)
	}
}
