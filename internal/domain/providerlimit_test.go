package domain_test

import (
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// TestProviderLimitBackoff pins the bounded exponential schedule. The
// function is pure: it reads no clock, so the table states the exact result
// for each attempt number.
func TestProviderLimitBackoff(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{name: "attempt below one clamps to the base delay", attempt: 0, want: time.Minute},
		{name: "negative attempt clamps to the base delay", attempt: -3, want: time.Minute},
		{name: "first attempt waits the base delay", attempt: 1, want: time.Minute},
		{name: "second attempt doubles", attempt: 2, want: 2 * time.Minute},
		{name: "third attempt doubles again", attempt: 3, want: 4 * time.Minute},
		{name: "fourth attempt doubles again", attempt: 4, want: 8 * time.Minute},
		{name: "fifth attempt doubles again", attempt: 5, want: 16 * time.Minute},
		{name: "sixth attempt reaches the cap", attempt: 6, want: 30 * time.Minute},
		{name: "later attempts stay at the cap", attempt: 40, want: 30 * time.Minute},
		{name: "very large attempts do not overflow", attempt: 1000, want: 30 * time.Minute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.ProviderLimitBackoff(tc.attempt); got != tc.want {
				t.Fatalf("ProviderLimitBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
			}
		})
	}
}

// TestProviderLimitBackoffConstantsAreExported pins the named constants, so a
// caller can report the schedule without repeating literal values.
func TestProviderLimitBackoffConstantsAreExported(t *testing.T) {
	if domain.ProviderLimitBackoffBase != time.Minute {
		t.Fatalf("ProviderLimitBackoffBase = %v, want 1m", domain.ProviderLimitBackoffBase)
	}
	if domain.ProviderLimitBackoffMax != 30*time.Minute {
		t.Fatalf("ProviderLimitBackoffMax = %v, want 30m", domain.ProviderLimitBackoffMax)
	}
}

// TestIssueProviderLimitRetryAtIsOptional proves the scheduling field is nil
// for an Issue that never met a provider limit.
func TestIssueProviderLimitRetryAtIsOptional(t *testing.T) {
	issue := domain.Issue{ID: "1", State: domain.StateReady}
	if issue.ProviderLimitRetryAt != nil {
		t.Fatalf("ProviderLimitRetryAt = %v, want nil", issue.ProviderLimitRetryAt)
	}

	due := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	issue.ProviderLimitRetryAt = &due
	if issue.ProviderLimitRetryAt == nil || !issue.ProviderLimitRetryAt.Equal(due) {
		t.Fatalf("ProviderLimitRetryAt = %v, want %v", issue.ProviderLimitRetryAt, due)
	}
}
