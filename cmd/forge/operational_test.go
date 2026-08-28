package main

import "testing"

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
