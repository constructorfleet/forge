# A worker-reported failure and a lost worker take different exits from the Engine

ADR 0020 makes two rules. A worker-reported gate or Agent failure is a normal failure. It must route through the Engine's existing failure handling. A lost worker (a lapsed heartbeat) must route to `LOST` and a budgeted retry. This ADR records how Forge tells the two cases apart, at the one point they could mix together: an error from the Remote backend's `WorkerClient` calls.

## The problem

`WorkerClient.Execute` and `WorkerClient.RunAgent` return a `(Result, error)` pair. A worker-reported failure is a value in that pair. It is a failing `execution.Result.ExitCode`, or an `agent.AgentResult.Status` of `StatusFailed`, with a nil error. A transport error, where the worker does not respond, is the non-nil error instead.

Before this ticket, the Engine treated every such error the same way. It folded a gate-command error into a synthetic failing `Result` in `runQualityGate`. It routed an Agent error straight to `failOut`, which transitions the Issue to `FAILED`. A lost worker's transport error and a genuine transport fault looked the same to the Engine.

## The fix: check the lease, not the error

The Remote backend's `environment` and `remoteAgent` types hold a `RecoverFunc` (`internal/execution/remote/remote.go`). On a non-nil `WorkerClient` error, each one calls `RecoverFunc` before it returns. `RecoverFunc` checks the `ExecutionLease`. It runs `RecoverLostExecution` (ADR 0020, ADR 0023) only when the lease has lapsed. Only then does the returned error wrap `execution.ErrLost` (`internal/execution/execution.go`). Every other error passes through unchanged. This includes every error when no `RecoverFunc` is configured.

A worker-reported failure never reaches this check. It carries no error, so `RecoverFunc` is never called. The branch depends on the ExecutionLease's state, never on an error's shape or message. This is what keeps the two cases apart.

## The Engine's one change: `failOut` recognizes `ErrLost`

`RecoverLostExecution` already leaves the Issue's `IssueState` unchanged. It advances the Issue's retry budget instead (ADR 0020, ADR 0023). If `failOut` still ran its ordinary FAILED/CANCELLED transition after that, it would undo this guarantee.

`failOut` (`internal/engine/engine.go`) now returns immediately when `origErr` wraps `execution.ErrLost`. It attempts no Workspace cleanup and no state transition in this case. Recovery already did the only bookkeeping a lost worker needs. The worker is presumably unreachable, so `failOut` has nothing left to clean up.

This is a narrow addition to the pattern `failOut` already used for `context.Canceled`: it dispatches on an error's identity, not on which backend produced the error. The Engine still does not need to know about the Remote backend, leases, or `WorkerClient`. It depends only on `execution.ErrLost`, a sentinel in the same neutral `execution` package that defines the `ExecutionEnvironment` seam. ADR 0020's "no Engine changes" meant the Engine's driving logic needs no per-backend branches. This change preserves that: LocalHost and Container never produce `ErrLost`, so this code path never runs for them.

## Out of scope: Quality Gates

Quality-Gate execution (`runQualityGate`) does not yet carry this same distinction. It still folds any `env.Execute` error into a synthetic failing gate result, even one that wraps `ErrLost`. This is unchanged, pre-existing behavior, not a regression from this ticket. A follow-up ticket can extend the distinction to `runQualityGate` once its return shape can carry a lost-worker signal.
