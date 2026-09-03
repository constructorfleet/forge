package clicommon

import "testing"

func TestDetectProviderLimit_MatchesKnownPattern(t *testing.T) {
	ok, line := DetectProviderLimit("", "Error: rate limit exceeded, please try again later")
	if !ok {
		t.Fatalf("DetectProviderLimit: ok = false, want true")
	}
	if line != "Error: rate limit exceeded, please try again later" {
		t.Errorf("line = %q, want the matched line verbatim", line)
	}
}

func TestDetectProviderLimit_CaseInsensitive(t *testing.T) {
	ok, _ := DetectProviderLimit("QUOTA EXCEEDED for this billing period")
	if !ok {
		t.Fatalf("DetectProviderLimit: ok = false, want true for uppercase match")
	}
}

func TestDetectProviderLimit_NoMatchReturnsFalse(t *testing.T) {
	ok, line := DetectProviderLimit("codex adapter: no structured result found in output", "some stderr")
	if ok {
		t.Fatalf("DetectProviderLimit: ok = true, want false; matched line %q", line)
	}
}

func TestDetectProviderLimit_EmptyInputsReturnFalse(t *testing.T) {
	ok, _ := DetectProviderLimit("", "")
	if ok {
		t.Fatalf("DetectProviderLimit: ok = true, want false for empty inputs")
	}
}

func TestProviderLimitSummary_NamesBackendAndLine(t *testing.T) {
	got := ProviderLimitSummary("codex", "Error: rate limit exceeded")
	want := "codex adapter: provider limit reached: Error: rate limit exceeded"
	if got != want {
		t.Errorf("ProviderLimitSummary() = %q, want %q", got, want)
	}
}
