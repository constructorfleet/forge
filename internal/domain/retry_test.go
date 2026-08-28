package domain_test

import (
	"testing"

	"github.com/Teagan42/forge/internal/domain"
)

func TestRetryBudgetCountersAreIndependent(t *testing.T) {
	budget := domain.NewRetryBudget(domain.RetryLimits{Gate: 3, Review: 2, CI: 3})

	if budget.RemainingGate() != 3 || budget.RemainingReview() != 2 || budget.RemainingCI() != 3 {
		t.Fatalf("unexpected initial remaining counts: %+v", budget)
	}

	if err := budget.RecordGateFailure(); err != nil {
		t.Fatalf("unexpected error recording gate failure: %v", err)
	}

	// Recording a gate failure must not touch review or CI counters.
	if budget.RemainingGate() != 2 {
		t.Fatalf("expected gate remaining 2, got %d", budget.RemainingGate())
	}
	if budget.RemainingReview() != 2 {
		t.Fatalf("review counter must be unaffected by gate failure, got %d", budget.RemainingReview())
	}
	if budget.RemainingCI() != 3 {
		t.Fatalf("CI counter must be unaffected by gate failure, got %d", budget.RemainingCI())
	}
}

func TestRetryBudgetExhaustion(t *testing.T) {
	budget := domain.NewRetryBudget(domain.RetryLimits{Gate: 1, Review: 1, CI: 1})

	if err := budget.RecordReviewRejection(); err != nil {
		t.Fatalf("unexpected error on first review rejection: %v", err)
	}
	if !budget.ReviewExhausted() {
		t.Fatalf("expected review budget to be exhausted after 1 rejection with limit 1")
	}

	err := budget.RecordReviewRejection()
	if err == nil {
		t.Fatalf("expected error recording review rejection past the configured ceiling")
	}
	if budget.RemainingGate() != 1 {
		t.Fatalf("gate counter must be unaffected by review rejections, got %d", budget.RemainingGate())
	}
}

func TestRetryBudgetCIIndependentFromGateAndReview(t *testing.T) {
	budget := domain.NewRetryBudget(domain.RetryLimits{Gate: 2, Review: 2, CI: 2})

	if err := budget.RecordCIFailure(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := budget.RecordCIFailure(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !budget.CIExhausted() {
		t.Fatalf("expected CI budget exhausted after 2 failures with limit 2")
	}
	if budget.GateExhausted() || budget.ReviewExhausted() {
		t.Fatalf("gate and review budgets must remain untouched by CI failures")
	}
}

// TestRetryBudgetRoundTripsThroughReconstruction proves a budget can be
// persisted (limits + counts read back out) and rehydrated into an
// identical budget, as ticket 13 (SQLite persistence) requires.
func TestRetryBudgetRoundTripsThroughReconstruction(t *testing.T) {
	limits := domain.RetryLimits{Gate: 3, Review: 2, CI: 3}
	original := domain.NewRetryBudget(limits)

	if err := original.RecordGateFailure(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := original.RecordGateFailure(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := original.RecordReviewRejection(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rehydrated := domain.NewRetryBudgetFrom(original.Limits(), original.GateFailures(), original.ReviewFailures(), original.CIFailures())

	if rehydrated.Limits() != original.Limits() {
		t.Fatalf("limits did not round-trip: got %+v, want %+v", rehydrated.Limits(), original.Limits())
	}
	if rehydrated.GateFailures() != original.GateFailures() {
		t.Fatalf("gate failures did not round-trip: got %d, want %d", rehydrated.GateFailures(), original.GateFailures())
	}
	if rehydrated.ReviewFailures() != original.ReviewFailures() {
		t.Fatalf("review failures did not round-trip: got %d, want %d", rehydrated.ReviewFailures(), original.ReviewFailures())
	}
	if rehydrated.CIFailures() != original.CIFailures() {
		t.Fatalf("CI failures did not round-trip: got %d, want %d", rehydrated.CIFailures(), original.CIFailures())
	}

	if rehydrated.RemainingGate() != original.RemainingGate() ||
		rehydrated.RemainingReview() != original.RemainingReview() ||
		rehydrated.RemainingCI() != original.RemainingCI() {
		t.Fatalf("remaining counts did not round-trip: got %+v, want %+v", rehydrated, original)
	}
	if rehydrated.GateExhausted() != original.GateExhausted() ||
		rehydrated.ReviewExhausted() != original.ReviewExhausted() ||
		rehydrated.CIExhausted() != original.CIExhausted() {
		t.Fatalf("exhausted flags did not round-trip: got %+v, want %+v", rehydrated, original)
	}
}

// TestRetryBudgetRemainingClampsToZero proves Remaining* never goes negative,
// even if reconstruction rehydrates a used count above its limit (e.g. the
// limit was lowered in config after failures were already recorded).
func TestRetryBudgetRemainingClampsToZero(t *testing.T) {
	budget := domain.NewRetryBudgetFrom(domain.RetryLimits{Gate: 1, Review: 1, CI: 1}, 5, 5, 5)

	if budget.RemainingGate() != 0 || budget.RemainingReview() != 0 || budget.RemainingCI() != 0 {
		t.Fatalf("expected remaining counts to clamp to zero, got %+v", budget)
	}
	if !budget.GateExhausted() || !budget.ReviewExhausted() || !budget.CIExhausted() {
		t.Fatalf("expected all counters to report exhausted, got %+v", budget)
	}
}

func TestRetryExhaustedErrorMessage(t *testing.T) {
	err := &domain.RetryExhaustedError{Class: "gate", Limit: 3}
	want := "retry budget exhausted: gate ceiling of 3 already reached"
	if err.Error() != want {
		t.Fatalf("unexpected error message: got %q, want %q", err.Error(), want)
	}
}
