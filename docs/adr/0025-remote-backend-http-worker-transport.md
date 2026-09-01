# The Remote backend's one real transport is a plain HTTP+JSON worker daemon

Issue #345 (ticket 7 of 7 under #287) adds the one concrete `WorkerClient`
transport: a worker daemon that answers the protocol over a real network
connection, not the in-memory `FakeWorker` every earlier ticket tested
against. This ADR records the transport's shape and why it changes nothing
above the `WorkerClient` seam.

## `httpworker.Server` and `httpworker.Client`, both behind `WorkerClient`

`internal/execution/remote/httpworker` adds two types. `Server` is the
worker daemon: an `http.Handler` that answers six operations
(`PrepareWorkspace`, `Execute`, `RunAgent`, `Heartbeat`, `FetchResult`,
`Cleanup`) as plain HTTP POST endpoints with JSON bodies, plus a `GET
/v1/health` check. `Client` implements `remote.WorkerClient` by calling
those endpoints. No new dependency is required: the wire format is
`encoding/json` over `net/http`, matching every other transport-free
package in Forge.

Neither type changes `remote.WorkerClient`, `remote.Backend`, or the
Engine. `Client` satisfies the same interface `remote.FakeWorker` does
(`var _ remote.WorkerClient = (*Client)(nil)`), so `remote.NewBackend`
drives it exactly as it drives the fake — this is the point of the seam
ADR 0020 introduced.

## The daemon owns its own git clone; the controller only sends a commit

`Server` is constructed with a local path to its own clone of the
repository and the name of the Git remote it may fetch from (read-only —
it never pushes, preserving the credential boundary from ADR 0020 and
issue #340). `PrepareWorkspace` fetches the requested base commit from that
remote, then creates a worktree for it via the same
`internal/workspace.Manager` LocalHost uses — the worker's Workspace is a
real git worktree, prepared the same way, just on a separate host's clone
instead of the controller's checkout.

This mirrors the Container backend's stance (ADR 0021): reuse the existing
worktree machinery rather than invent a second one, and keep base
resolution and pinning controller-side (`WorkspaceRequest.Base` is the only
input `PrepareWorkspace` needs).

## `Execute` and `RunAgent` reproduce LocalHost's subprocess semantics

`Server.Execute` runs a `Command` with the identical subprocess handling
LocalHost's `environment.Execute` uses (shell or direct argv, `WaitDelay`,
captured stdout/stderr, exit code translation) — the point being that a
Command sent over HTTP behaves exactly as one run in-process, which is what
the acceptance criterion "the integration test confirms the observable
outcome matches the fake-backed unit behavior" requires. `Server.RunAgent`
overrides `AgentRequest.WorkspacePath` with the worker's own path before
calling its Agent: the controller cannot know the worker's local filesystem
layout in advance.

## `FetchResult` bundles `base..HEAD` from the worker's own worktree

`Server.FetchResult` runs `git bundle create <tmp> <base>..HEAD` inside the
prepared worktree, then returns the bundle bytes and `HEAD`'s SHA — the
same `WorkerResult` shape and the same bundle range `BundlePublisher`
(issue #340) already expects, now produced by a real subprocess instead of
a test fixture. The range's tip must be spelled `HEAD`, not a literal SHA:
`git bundle create` refuses to record a bare commit as a ref-less tip.

## A transport error is still just an error to `RecoverFunc`

`Client`'s HTTP failures (connection refused, timeout, a non-2xx response)
surface as ordinary Go errors from `WorkerClient` calls, with no special
shape. `remote.environment` and `remoteAgent` classify them through
`RecoverFunc` exactly as they classify a `FakeWorker`'s programmed error
(ADR 0024): only a lapsed `ExecutionLease` turns one into `execution.
ErrLost`. The integration test proves this by closing the daemon mid-run
and confirming the resulting error wraps `execution.ErrLost` once recovery
says the worker is lost — the real transport does not need its own loss
model, because the existing lease-based one already covers it.

## Wiring: `buildWorkerClient` now Pings the configured endpoint

`cmd/forge/wiring.go`'s `buildWorkerClient` (issue #343) constructs an
`httpworker.Client` against `cfg.Execution.Worker.Endpoint` and calls
`Client.Ping`, a bounded-timeout `GET /v1/health`. A worker that does not
answer still fails preflight with `remote.ErrWorkerUnreachable`, wrapping
the real transport error — the same failure mode this preflight always
had, now backed by an actual reachability check instead of an
unconditional stub.
