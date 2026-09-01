# The Container backend bind-mounts a host worktree; publication stays host-side

Issue #286 adds a **Container** `ExecutionBackend` (ADR 0020's sibling to the Remote backend): it runs the Agent and Quality Gates inside an isolated container, instead of in-process or on a separate worker host. Issue #334 (ticket 1 of 5 under #286) adds the `Prepare`/`Cleanup` lifecycle ends and a thin `ContainerRuntime` seam. This ADR records the runtime abstraction's shape, the bind-mount design, and why publication stays on the host.

## One `ContainerRuntime` seam, not a direct daemon dependency

The Container backend talks to containers through one `ContainerRuntime` interface (`internal/execution/container`): start a container from a `ContainerSpec` (image, bind mounts), run a Command inside it, stop it, and remove it. One interface keeps the backend independent of any particular container daemon and keeps it fakeable in tests: `FakeRuntime` simulates the lifecycle in memory, and a conformance test drives the Container backend through it, mirroring the LocalHost (ADR from #285) and Remote (ADR 0020) conformance tests.

`Prepare` reuses `internal/workspace.Manager` — the same git-worktree machinery LocalHost uses — to create the host Workspace, then calls `ContainerRuntime.Start` with that Workspace's path bind-mounted at a fixed container path (`WorkspaceMountPath`). `Cleanup` stops and removes the container, then cleans up the worktree through the same Manager. This ticket only wires the lifecycle ends; a later ticket adds `ContainerRuntime.Exec`-backed command execution and the Agent through this environment.

## The bind mount shares the host repository's git object store

A git worktree, unlike a clone, has no object store of its own: it is a working directory and index pointing back at the main repository's `.git`. Bind-mounting that worktree into the container, instead of copying or cloning it, means the container's git operations reach the exact same object store as the host repository, with zero extra fetch or copy step.

This is a deliberate, load-bearing property, not an incidental one: later tickets in #286 (and the design in #286's parent body) rely on it for host-side publication (see below). Ticket #334's conformance test asserts it directly: a commit made on the host repository, on a branch the Workspace's worktree never checked out, is immediately visible from inside the Workspace through `git cat-file`, with no fetch.

## Publication (push, pull request, tracker updates) stays host-side, never enters the container

The container gets no Git remote credentials and makes no push, pull-request, or tracker call itself. Every publishing action — pushing the Worker's branch, opening or updating a pull request, writing back to the issue tracker — runs on the host, against the bind-mounted worktree's branch, after the container's work finishes and the container is torn down.

This mirrors the Remote backend's split of authority (ADR 0020): a disposable executor holds no scheduling or publication authority, and the controller (here, the host process) keeps every credential and side effect that reaches outside the sandboxed workspace. For Container specifically, this also keeps the container's threat surface small: an image only needs read/write access to files under `WorkspaceMountPath`, with no network credentials to steal or misuse.

## This ticket ships Prepare/Cleanup only

Ticket #334 adds the `ContainerRuntime` seam, `Prepare`, and `Cleanup`, with a `FakeRuntime` and a conformance test proving the arc over it. `environment.Execute` and `environment.Agent` are not implemented yet; they return a placeholder "not implemented" error and a nil Agent respectively until a later ticket under #286 wires `ContainerRuntime.Exec` and an in-container Agent adapter. Container image and resource configuration, and container-specific failure handling, are also later tickets.
