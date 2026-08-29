package clicommon

import (
	"strings"
	"testing"
)

func TestTruncate_ShorterThanLimitIsUnchanged(t *testing.T) {
	if got := Truncate("hello", 10); got != "hello" {
		t.Errorf("Truncate() = %q, want %q", got, "hello")
	}
}

func TestTruncate_LongerThanLimitIsCutAndMarked(t *testing.T) {
	got := Truncate("0123456789", 4)
	if got[:4] != "0123" {
		t.Errorf("Truncate() = %q, want to start with 0123", got)
	}
	if len(got) <= 4 {
		t.Errorf("Truncate() = %q, want a truncation marker appended", got)
	}
}

func TestDiagnosticSummary_FoldsInStdoutAndStderr(t *testing.T) {
	got := DiagnosticSummary("prefix", "out text", "err text")
	for _, want := range []string{"prefix", "out text", "err text"} {
		if !strings.Contains(got, want) {
			t.Errorf("DiagnosticSummary() missing %q, got:\n%s", want, got)
		}
	}
}

func TestDiagnosticSummary_OmitsEmptyStreams(t *testing.T) {
	got := DiagnosticSummary("prefix", "", "")
	if got != "prefix" {
		t.Errorf("DiagnosticSummary() = %q, want just the prefix", got)
	}
}
