# A provider limit gets its own Issue state, its own retry counter, and an automatic backoff retry

A model provider can apply a rate or quota limit to an Agent. The Agent then stops with no work done and no defect in the work it did. Forge must wait and try again. It must not repair, and it must not fail the Issue on the first stop.

This ADR records three decisions: a dedicated `PROVIDER_LIMIT` Issue state, a fourth independent retry-budget class, and a controller that retries on a bounded exponential backoff.

## Why a dedicated state, not FAILED with a flag

`FAILED` is terminal. An Issue in `FAILED` starts no further automatic work, and only a human retry moves it back to `READY`. A provider limit is transient, so a terminal state is the wrong resting place.

A flag on `FAILED` would express the difference, but it would break the state machine's central rule: the state alone tells a reader, and every scheduler, what may happen next. Every place that reads `IsTerminal` would then need to read the flag too, and one place that forgot would either strand a recoverable Issue or restart a dead one.

`PROVIDER_LIMIT` is a non-terminal state with a normal entry in the transitions table, exactly like `NEEDS_INFO` and `NEEDS_REPLAN`. `ValidateTransition` needs no special case for it. The state machine grows from 17 states to 18.

The entry point is `IMPLEMENTING` only. That is the one stage whose agent-status switch (`internal/engine/engine.go`) turns an Agent-reported outcome into an Issue state. The exits are `READY` (the automatic retry) and `FAILED` (the retry budget is exhausted), plus the generic `CANCELLED` edge every non-terminal state has.

### Why the review path is out of scope

`REVIEWING` also routes to `FAILED` on an Agent failure, so a review-side entry point looked plausible. It is not. `internal/review/agentreviewer` consumes an `agent.AgentResult` per review axis, inside the Reviewer, and turns it into an axis envelope. It never maps a status onto an Issue transition. The Reviewer already retries an unrecoverable axis in place, a bounded number of times, and reports coverage honestly when an axis stays unrecoverable.

There is therefore no existing seam on the review path that consumes an `agent.AgentStatus` at issue-transition granularity. Building one would mean inventing a new integration, not wiring an existing one. `PROVIDER_LIMIT` is reachable from `IMPLEMENTING` alone until such a seam exists.

## Why a fourth retry counter, not the gate counter

ADR 0023 makes a lost worker reuse the gate counter. That is right for a lost worker: it is an incomplete attempt at the same work, exactly like a gate failure, and both must redo that work.

A provider limit is a different thing. It says nothing about the Agent's work. The work may be perfect and still stop, because a quota window closed. Charging it to the gate counter would let an unrelated external condition spend the budget a real gate failure needs, and an Issue could reach `FAILED` with no failed gate anywhere in its history.

ADR 0007's rule decides this: different failure classes get independent ceilings, so ordinary churn in one class never exhausts another. A provider limit is a distinct class, so it gets a distinct counter.

`domain.RetryBudget` gains a fourth `retryCounter`, `providerLimit`, with `RecordProviderLimitStop`, `RemainingProviderLimit`, `ProviderLimitExhausted`, and `ProviderLimitFailures`. `RetryLimits` gains `ProviderLimit` (yaml `retry.provider_limit`), which defaults to 3, the same as the gate ceiling.

A ceiling of N tolerates N provider-limit stops for one Issue. The Nth stop is terminal, so Forge makes N-1 automatic retries.

## The backoff schedule

`domain.ProviderLimitBackoff` is a pure function of the attempt number. It reads no clock: the caller adds the result to its own "now" value, which keeps the schedule deterministic and lets a table-driven test pin every value.

The attempt number is the 1-based `RetryBudget.ProviderLimitFailures` count, read after the current stop is recorded.

The schedule doubles from `ProviderLimitBackoffBase` and stops at `ProviderLimitBackoffMax`: 1m, 2m, 4m, 8m, 16m, then 30m for every later attempt. Both constants are named and exported, so a reader finds them in one place and an operator can see what the code will do.

One minute is the base because a provider usually clears a rate window in about that time, so a shorter first wait only spends budget. Thirty minutes is the cap because a quota that a longer wait cannot clear needs a human, and the retry budget ends the loop anyway.

Forge never schedules a wait it cannot spend. `handleProviderLimit` (`internal/engine/providerlimit.go`) fails the Issue at once when the recorded stop exhausts the budget, instead of parking it for a backoff that no retry can follow.

## The controller

`engine.ProviderLimitController` (`internal/engine/providerlimitcontroller.go`) has the same shape as `engine.LostExecutionController`: a `Now` clock, an overridable `Sleep`, a `ReconcileOnce` pass, and a `Run` loop that keeps going after a failed pass. Both controllers recover work that stopped for a reason outside the Agent's control, so they share one model.

Each pass calls `Store.ListDueProviderLimitIssues`, a cross-Execution query for Issues in `PROVIDER_LIMIT` whose `provider_limit_retry_at` has passed. This mirrors `ListActiveExecutionLeases`: the loop finds parked work without knowing Execution IDs in advance.

For each due Issue the pass transitions it to `READY` and clears the deadline. It then calls `ExecutionResumer.ResumeExecution` once per distinct Execution with at least one retried Issue, which is how the retried Issue re-enters Prepare/Execute. A due Issue whose budget has no room left goes to `FAILED` instead; this is a guard for a ceiling that configuration lowered between runs.

`forge execute` starts the controller for every backend, unlike the lost-execution controller, which the Remote backend alone needs. A provider limit can stop an Agent under every backend. The poll interval is 15 seconds, shorter than the base backoff, so a due Issue starts again promptly.

`forge resume` leaves a parked Issue exactly as it finds it (`internal/engine/recovery.go`). The controller owns the single exit from `PROVIDER_LIMIT`.

## Persistence

Migration `0026_provider_limit_retry.sql` adds three columns to `execution_issues`: `retry_provider_limit_limit`, `retry_provider_limit_used`, and the nullable `provider_limit_retry_at`.

`Store.UpdateRetryBudget` absorbs the new counter, because a counter is what that method already persists. The deadline gets its own narrow method, `Store.ScheduleProviderLimitRetry`, because a deadline is a scheduling fact rather than a counter, and because a different caller writes it at a different time: the Engine schedules the deadline, and the controller clears it.

## The boundary with adapter-level detection

This ADR covers only what happens after `agent.StatusProviderLimit` is reported. Producing that status from live provider output — parsing CLI adapter output for rate-limit and quota signals — is separate work, tracked in issue 416.

`agent.StatusProviderLimit` therefore exists as a consumable status before every producer path is wired, the same way `StatusReplanRequired` and `StatusNeedsInfo` did. The state, the budget, the backoff, and the controller are all reachable and tested end to end through that status today. No CLI adapter changes are part of this decision.
