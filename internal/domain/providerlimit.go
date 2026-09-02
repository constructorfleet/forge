package domain

import "time"

// ProviderLimitBackoffBase is the wait before the first automatic retry of an
// Issue parked in PROVIDER_LIMIT. A model provider usually clears a rate
// window in about one minute, so a shorter first wait only spends budget.
const ProviderLimitBackoffBase = time.Minute

// ProviderLimitBackoffMax is the largest wait ProviderLimitBackoff returns.
// A quota that a longer wait cannot clear needs a human, and the
// provider-limit retry budget ends the wait loop anyway, so a longer cap adds
// delay without adding value.
const ProviderLimitBackoffMax = 30 * time.Minute

// providerLimitBackoffCapAttempt is the first attempt number whose doubled
// delay reaches ProviderLimitBackoffMax. The doubling stops here, so a very
// large attempt number cannot overflow the shift.
const providerLimitBackoffCapAttempt = 6

// ProviderLimitBackoff reports how long to wait before the automatic retry of
// an Issue that stopped on a provider rate or quota limit. attempt is the
// 1-based count of provider-limit stops recorded for that Issue, which is
// RetryBudget.ProviderLimitFailures after the current stop is recorded.
//
// The schedule doubles from ProviderLimitBackoffBase and stops at
// ProviderLimitBackoffMax: 1m, 2m, 4m, 8m, 16m, then 30m for every later
// attempt. An attempt below 1 gets the base delay, so a caller that has not
// yet recorded a stop still waits.
//
// The function is pure. It reads no clock. The caller adds the result to its
// own "now" value, which keeps the schedule deterministic and testable.
func ProviderLimitBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt >= providerLimitBackoffCapAttempt {
		return ProviderLimitBackoffMax
	}
	delay := ProviderLimitBackoffBase << (attempt - 1)
	if delay > ProviderLimitBackoffMax {
		return ProviderLimitBackoffMax
	}
	return delay
}
