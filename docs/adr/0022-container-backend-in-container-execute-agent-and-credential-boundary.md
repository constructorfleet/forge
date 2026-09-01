# The Container backend runs Execute and the Agent in-container, through one AgentFactory seam

Issue #335 (ticket 2 of 5 under #286) fills in `environment.Execute` and `environment.Agent`, which ADR 0021 (issue #334) left as placeholders. This ADR records the design.

## Execute runs through ContainerRuntime.Exec, with no translation

`environment.Execute` forwards its Command directly to `ContainerRuntime.Exec`. The Container backend does not translate `Command.WorkDir` between a host path and a container path: `WorkDir` is already relative to the Workspace root, and `ContainerRuntime.Exec` is responsible for resolving that root to `WorkspaceMountPath` inside its container. This keeps `environment.Execute` a one-line pass-through, and keeps Quality Gates (which already call `Execute` through `internal/engine`, unchanged since ADR 0021) in-container automatically, with no Engine changes.

## Command gains Args, Stdin, and Env

`execution.Command` gains three fields this ticket needs: `Args` (an argv to run directly, with no shell), `Stdin`, and `Env`. `Args` matters most for the Agent: a CLI Agent Adapter's arguments (a JSON schema, a prompt) are not safe to embed in a shell string, so `Args` avoids that class of bug entirely rather than adding a shell-quoting helper. LocalHost's `Execute` also honors these fields now, so `execution.Command`'s contract is the same across backends, not Container-only.

## The Agent runs through an AgentFactory, not a fixed instance

LocalHost's `Backend` is constructed with one already-built `agent.Agent`: the backend does not know how the Agent runs. The Container backend instead takes an `AgentFactory` (`func(env execution.ExecutionEnvironment) agent.Agent`), because building the Agent needs the environment's `Execute` (and, through it, the running container). `environment.Agent()` calls the factory with itself.

`NewAgentRunner(env, executable)` is the seam a factory uses to wire an existing CLI Agent Adapter (`internal/agent/claude.Adapter`, or any `internal/agent/clicommon`-based Adapter) to run in-container: it matches those Adapters' `Runner` field signature, and turns each invocation into one `Execute` call carrying the CLI's argv, stdin (the prompt), and sanitized env. This reuses every CLI Adapter's existing prompt-building, result-parsing, and transcript logic unchanged; only the subprocess launch moves from `os/exec` to `Execute`. Wiring a specific Adapter (`claude`, `codex`, ...) into `NewBackend`'s `AgentFactory` is issue #336's concern (config, wiring, and preflight), not this ticket's.

## FakeRuntime.Exec runs the command for real, against the bind-mounted host directory

Since #334 established that the Container backend's Workspace is a host bind mount, `FakeRuntime.Exec` simulates "run inside the container" by actually running the command, on the host, inside the host directory the `ContainerSpec` bind-mounted at `WorkspaceMountPath`. This is a more faithful simulation than a purely in-memory double: it lets tests observe real effects — a `git commit` run through `Execute` lands in the host repository's object store, exactly as this ticket's acceptance criteria require — without needing a live container daemon, which remains out of scope for #286.

## Repository Context needed no change

`repocontext.Compile` already reads only from the filesystem at a given repository root (no git shell-outs), and the Container backend's `Workspace().Path` is already a host path into the bind-mounted worktree. Compiling from `env.Workspace().Path`, exactly as `internal/engine` already does for every backend, already reflects whatever the container itself writes into the Workspace. This ticket adds a test proving that property; it needed no implementation change.

## The credential boundary is proven by observed behavior, not asserted by inspection

No credential ever reaches `ContainerRuntime.Exec`: Quality Gates never populate `Command.Env` from Forge's own process environment, and a CLI Adapter's `SanitizedEnv` only ever forwards its own declared allowlist (LLM auth variables), never SCM or tracker credentials. Publication (push, pull request, tracker updates) stays host-side per ADR 0021 and never calls `Execute` at all. This ticket's credential-boundary test sets a tracker-credential-shaped environment variable in the test process, runs a Quality Gate and an Agent invocation, and asserts none of `FakeRuntime`'s recorded calls carry it; a second test proves a push-equivalent host git operation never adds an entry to `FakeRuntime`'s call log.
