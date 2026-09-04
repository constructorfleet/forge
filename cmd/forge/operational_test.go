package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

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
