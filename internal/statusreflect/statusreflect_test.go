package statusreflect_test

import (
	"context"
	"testing"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/statusreflect"
	"github.com/Teagan42/forge/internal/tracker"
)

func enabledConfig() config.StatusReflectionConfig {
	return config.StatusReflectionConfig{
		Enabled:         true,
		InProgressLabel: "in-progress",
		InReviewLabel:   "in-review",
		BlockedLabel:    "blocked",
		FailedLabel:     "failed",
		Comment:         true,
	}
}

// TestLabel pins the full canonical IssueState -> tracker label mapping
// (domain.IssueState.Group), one row per state so every bucket is explicit
// and a state omitted from the mapping is caught.
func TestLabel(t *testing.T) {
	cfg := enabledConfig()

	cases := []struct {
		state domain.IssueState
		want  string
	}{
		{domain.StatePending, ""},
		{domain.StateBlockedDependency, ""},
		{domain.StateReady, ""},
		{domain.StateClaimed, "in-progress"},
		{domain.StatePreparing, "in-progress"},
		{domain.StateImplementing, "in-progress"},
		{domain.StateValidating, "in-progress"},
		{domain.StateReviewing, "in-progress"},
		{domain.StateCommitting, "in-progress"},
		{domain.StatePRCreating, "in-progress"},
		{domain.StateCIPending, "in-review"},
		{domain.StateCIFailed, "in-review"},
		{domain.StateNeedsInfo, "blocked"},
		{domain.StateNeedsReplan, "blocked"},
		{domain.StateProviderLimit, "blocked"},
		{domain.StateFailed, "failed"},
		{domain.StateDone, ""},
		{domain.StateCancelled, ""},
	}
	for _, c := range cases {
		if got := statusreflect.Label(cfg, c.state); got != c.want {
			t.Errorf("Label(%s) = %q, want %q", c.state, got, c.want)
		}
	}
}

func TestApply_DisabledIsNoOp(t *testing.T) {
	cfg := enabledConfig()
	cfg.Enabled = false
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateReady, domain.StateClaimed); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 0 {
		t.Errorf("Labels = %v, want none (disabled)", labels)
	}
}

func TestApply_NilTrackerIsNoOp(t *testing.T) {
	cfg := enabledConfig()
	if err := statusreflect.Apply(context.Background(), nil, cfg, "1", domain.StateReady, domain.StateClaimed); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestApply_ClaimAppliesInProgressLabel(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateReady, domain.StateClaimed); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "in-progress" {
		t.Errorf("Labels = %v, want [in-progress]", labels)
	}
}

func TestIsStartTransition(t *testing.T) {
	cfg := enabledConfig()

	if !statusreflect.IsStartTransition(cfg, domain.StateReady, domain.StateClaimed) {
		t.Error("IsStartTransition(READY, CLAIMED) = false, want true")
	}
	// PREPARING -> IMPLEMENTING both map to InProgressLabel, so this is a
	// same-label hop within active work, not the READY -> CLAIMED start.
	if statusreflect.IsStartTransition(cfg, domain.StatePreparing, domain.StateImplementing) {
		t.Error("IsStartTransition(PREPARING, IMPLEMENTING) = true, want false")
	}
	noComment := cfg
	noComment.Comment = false
	if statusreflect.IsStartTransition(noComment, domain.StateReady, domain.StateClaimed) {
		t.Error("IsStartTransition with Comment=false = true, want false")
	}
	disabled := cfg
	disabled.Enabled = false
	if statusreflect.IsStartTransition(disabled, domain.StateReady, domain.StateClaimed) {
		t.Error("IsStartTransition with Enabled=false = true, want false")
	}
}

func TestApply_PRCreatedSwapsToInReviewLabel(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StatePRCreating, domain.StateCIPending); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "in-review" {
		t.Errorf("Labels = %v, want [in-review]", labels)
	}
}

func TestApply_FailedSwapsToFailedLabel(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateImplementing, domain.StateFailed); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "failed" {
		t.Errorf("Labels = %v, want [failed]", labels)
	}
}

func TestApply_NeedsInfoSwapsToBlockedLabel(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateImplementing, domain.StateNeedsInfo); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "blocked" {
		t.Errorf("Labels = %v, want [blocked]", labels)
	}
}

func TestApply_ProviderLimitSwapsToBlockedLabel(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateImplementing, domain.StateProviderLimit); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "blocked" {
		t.Errorf("Labels = %v, want [blocked]", labels)
	}
}

// TestApply_ProviderLimitClearsBlockedLabelOnResume pins the other end of
// the PROVIDER_LIMIT hole: when the backoff clears and the Issue returns to
// READY, the blocked label is removed, not left behind.
func TestApply_ProviderLimitClearsBlockedLabelOnResume(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateProviderLimit, domain.StateReady); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 0 {
		t.Errorf("Labels = %v, want none (blocked label removed on resume)", labels)
	}
}

func TestApply_TerminalStatesClearLabel(t *testing.T) {
	cfg := enabledConfig()

	for _, to := range []domain.IssueState{domain.StateDone, domain.StateCancelled} {
		trk := tracker.NewFakeTracker()
		if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateCIPending, to); err != nil {
			t.Fatalf("Apply to %s: %v", to, err)
		}
		if labels := trk.Labels("1"); len(labels) != 0 {
			t.Errorf("to %s: Labels = %v, want none", to, labels)
		}
	}
}

func TestApply_RepeatedSameTransitionIsNoOp(t *testing.T) {
	cfg := enabledConfig()
	trk := tracker.NewFakeTracker()

	for i := 0; i < 3; i++ {
		if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateReady, domain.StateClaimed); err != nil {
			t.Fatalf("Apply #%d: %v", i, err)
		}
	}
	if labels := trk.Labels("1"); len(labels) != 1 || labels[0] != "in-progress" {
		t.Errorf("Labels = %v, want [in-progress] (idempotent)", labels)
	}
}

func TestApply_NoLabelConfiguredSkipsThatPhase(t *testing.T) {
	cfg := enabledConfig()
	cfg.InReviewLabel = ""
	trk := tracker.NewFakeTracker()

	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StateReady, domain.StateClaimed); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := statusreflect.Apply(context.Background(), trk, cfg, "1", domain.StatePRCreating, domain.StateCIPending); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if labels := trk.Labels("1"); len(labels) != 0 {
		t.Errorf("Labels = %v, want none (in-progress removed, no in-review configured to add)", labels)
	}
}
