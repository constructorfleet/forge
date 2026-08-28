package domain

import "fmt"

// RetryLimits configures the ceiling for each independent retry counter.
// See CONTEXT.md "Retry Budget": gate failures, review rejections, and CI
// failures are different failure classes with independent ceilings.
//
// The yaml tags let internal/config decode .forge.yaml's retry section
// directly onto this type rather than through a parallel config-only type;
// they impose no infrastructure dependency since struct tags are just
// string literals — this package still imports nothing beyond "time".
type RetryLimits struct {
	Gate   int `yaml:"gate"`
	Review int `yaml:"review"`
	CI     int `yaml:"ci"`
}

// retryCounter tracks failures against a single ceiling. RetryBudget holds
// three named instances — one per failure class — so the counting and
// exhaustion logic is written once and reused for gate, review, and CI.
type retryCounter struct {
	limit int
	used  int
}

func (c retryCounter) remaining() int {
	if r := c.limit - c.used; r > 0 {
		return r
	}
	return 0
}

func (c retryCounter) exhausted() bool { return c.used >= c.limit }

// record increments the counter, returning a *RetryExhaustedError tagged
// with class if the ceiling was already reached before this call.
func (c *retryCounter) record(class string) error {
	if c.exhausted() {
		return &RetryExhaustedError{Class: class, Limit: c.limit}
	}
	c.used++
	return nil
}

// RetryBudget tracks separate retry counters for quality-gate failures,
// review rejections, and CI failures. A shared counter would let ordinary
// development churn exhaust the budget prematurely, so each class has its
// own configurable ceiling.
type RetryBudget struct {
	gate   retryCounter
	review retryCounter
	ci     retryCounter
}

// NewRetryBudget builds a RetryBudget with fresh (zeroed) counters for the
// given limits.
func NewRetryBudget(limits RetryLimits) RetryBudget {
	return NewRetryBudgetFrom(limits, 0, 0, 0)
}

// NewRetryBudgetFrom reconstructs a RetryBudget from persisted counts — the
// path SQLite persistence (ticket 13) uses to rehydrate a budget whose
// counters are already non-zero.
func NewRetryBudgetFrom(limits RetryLimits, gateFailures, reviewFailures, ciFailures int) RetryBudget {
	return RetryBudget{
		gate:   retryCounter{limit: limits.Gate, used: gateFailures},
		review: retryCounter{limit: limits.Review, used: reviewFailures},
		ci:     retryCounter{limit: limits.CI, used: ciFailures},
	}
}

// RetryExhaustedError is returned when a failure is recorded past the
// configured ceiling for its class.
type RetryExhaustedError struct {
	Class string
	Limit int
}

func (e *RetryExhaustedError) Error() string {
	return fmt.Sprintf("retry budget exhausted: %s ceiling of %d already reached", e.Class, e.Limit)
}

// RecordGateFailure increments the gate-failure counter. It returns a
// *RetryExhaustedError if the ceiling was already reached before this call.
func (b *RetryBudget) RecordGateFailure() error { return b.gate.record("gate") }

// RecordReviewRejection increments the review-rejection counter. It returns
// a *RetryExhaustedError if the ceiling was already reached before this call.
func (b *RetryBudget) RecordReviewRejection() error { return b.review.record("review") }

// RecordCIFailure increments the CI-failure counter. It returns a
// *RetryExhaustedError if the ceiling was already reached before this call.
func (b *RetryBudget) RecordCIFailure() error { return b.ci.record("ci") }

// RemainingGate reports how many gate failures may still be recorded.
func (b RetryBudget) RemainingGate() int { return b.gate.remaining() }

// RemainingReview reports how many review rejections may still be recorded.
func (b RetryBudget) RemainingReview() int { return b.review.remaining() }

// RemainingCI reports how many CI failures may still be recorded.
func (b RetryBudget) RemainingCI() int { return b.ci.remaining() }

// GateExhausted reports whether the gate-failure ceiling has been reached.
func (b RetryBudget) GateExhausted() bool { return b.gate.exhausted() }

// ReviewExhausted reports whether the review-rejection ceiling has been reached.
func (b RetryBudget) ReviewExhausted() bool { return b.review.exhausted() }

// CIExhausted reports whether the CI-failure ceiling has been reached.
func (b RetryBudget) CIExhausted() bool { return b.ci.exhausted() }

// GateFailures reports the current gate-failure count, for persistence.
func (b RetryBudget) GateFailures() int { return b.gate.used }

// ReviewFailures reports the current review-rejection count, for persistence.
func (b RetryBudget) ReviewFailures() int { return b.review.used }

// CIFailures reports the current CI-failure count, for persistence.
func (b RetryBudget) CIFailures() int { return b.ci.used }

// Limits reports the configured ceilings, for persistence.
func (b RetryBudget) Limits() RetryLimits {
	return RetryLimits{Gate: b.gate.limit, Review: b.review.limit, CI: b.ci.limit}
}
