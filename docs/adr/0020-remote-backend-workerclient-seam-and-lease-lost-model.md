# The Remote backend drives one WorkerClient seam; a lease detects a lost worker

Issue #287 adds a **Remote** `ExecutionBackend` (ADR from #285's seam): it runs the Agent and Quality Gates on a separate, configured worker host, instead of in-process. Issue #339 (ticket 1 of 7 under #287) adds the seam and its happy path, with a fake worker. This ADR records three decisions: the shape of the controller-to-worker boundary, the split of authority between controller and worker, and the lease and `LOST` model that later tickets implement.

## One `WorkerClient` seam, not one interface per operation

The Remote backend talks to the worker through a single `WorkerClient` interface (`internal/execution/remote`): prepare a Workspace at a pinned commit, run a Command or the Agent there, heartbeat, fetch the finished result, and clean up. One interface keeps the transport swappable (a later ticket adds a real transport behind it) and keeps the backend fakeable in tests: ticket #339 adds a `FakeWorker` that implements `WorkerClient` in memory, and a conformance test drives the Remote backend through it, mirroring the LocalHost (#285) and Container (#286) conformance tests.

The Remote backend's `environment` (its `ExecutionEnvironment`) adapts `WorkerClient` calls to the existing `execution.ExecutionEnvironment` and `agent.Agent` seams. This means the Engine drives the Remote backend exactly as it drives LocalHost or Container: `Prepare`, then `Execute`/`Agent().Execute`/`Cleanup` on the returned environment, with no Engine changes.

## The controller keeps the canonical repository; the worker holds only the disposable Workspace

Base resolution, ancestry and reachability checks, and target-tip resolution stay controller-side, against the controller's canonical repository (ADR 0006, ADR 0011, ADR 0012 already assume this authority). The Remote backend passes the worker only a pinned commit (`WorkspaceRequest.Base`, unchanged from the LocalHost/Container seam); the worker fetches read-only at that exact commit and never resolves a base or a branch tip itself.

The worker is a disposable executor with no scheduling authority. It runs the Agent and Quality Gates in its own Workspace and reports results back; it never pushes, opens a change request, or mutates the tracker. A later ticket (#280's transport) has the worker return its result as a Git bundle the controller imports and publishes; this ticket's `WorkerResult` already carries the worker's Workspace, so later tickets add the bundle without changing the seam shape.

## A lease with a heartbeat and expiry detects a lost worker; `LOST` retries under the existing budget

Every remote execution holds an `ExecutionLease`, following the existing `PlanningLease` pattern (`internal/storage/planning.go`): claimed by execution and Issue identity, with a heartbeat timestamp and an expiry, enforced at the storage layer rather than read-then-write. The worker heartbeats through `WorkerClient.Heartbeat` while work is in progress. When the heartbeat lapses past the lease's expiry, the controller treats the execution as `LOST`: its Workspace becomes non-authoritative, and the Issue retries under its existing retry budget (ADR 0007), not a new bespoke one.

`LOST` is an execution-substrate state, not an `IssueState`. A worker-reported gate or Agent failure is a normal failure and routes through the Engine's existing failure handling; only a worker that stops heartbeating drives an execution to `LOST`. This keeps the Issue state machine unchanged and keeps the distinction between "the worker failed the work" and "the worker vanished" explicit.

This ticket (#339) records the lease and `LOST` model but does not implement lease storage or the heartbeat-expiry check; it ships the happy path only, against a fake worker that always succeeds. A later ticket under #287 adds the `ExecutionLease` storage and the `LOST` transition.
