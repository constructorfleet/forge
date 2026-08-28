Status: ready-for-agent

# Forge MVP Spec

## Problem Statement

Current coding-agent workflows repeatedly spend context and tokens discovering deterministic facts and procedures — where issues live, what quality commands to run, how to create branches, when to open PRs, which checks to watch. A developer who wants multiple issues worked concurrently must narrate the entire workflow in natural language every time, producing prompts that are mostly policy rather than engineering intent. The desired interface is `forge execute 345 344 343` — everything else should come from repository and runtime configuration.

## Solution

A deterministic orchestration layer — Forge — that owns workflow mechanics and invariants while coding agents own engineering judgment. Given a set of issue numbers, Forge autonomously: resolves dependencies, schedules concurrent work, creates isolated Workspaces, compiles Repository Context once, invokes coding Agents with normalized Execution Context, runs Quality Gates, routes failures back to Workers with bounded diagnostics, conducts independent Review, commits, creates PRs, monitors CI, repairs CI failures, and marks information-blocked Issues for human input. The orchestrator is a single Go binary backed by SQLite, invoked via CLI.

## User Stories

1. As a developer, I want to run `forge execute 345 344 343` and have all three Issues worked autonomously, so that I don't narrate workflow mechanics in every prompt.
2. As a developer, I want Forge to resolve Dependencies between Issues automatically, so that dependent work only begins after prerequisite PRs are merged.
3. As a developer, I want independent Issues to execute concurrently up to a configured limit, so that wall-clock time is minimized.
4. As a developer, I want each Issue to get an isolated Workspace, so that concurrent Workers never interfere with each other or the primary checkout.
5. As a developer, I want Repository Context compiled once per Execution, so that Workers don't independently spend tokens rediscovering quality commands and project structure.
6. As a developer, I want the Agent to receive only normalized Execution Context, so that it focuses on engineering judgment rather than workflow discovery.
7. As a developer, I want Quality Gates to run deterministically after implementation, so that lint, test, typecheck, format, and build failures are caught before any PR is created.
8. As a developer, I want gate failures routed back to the Worker with bounded diagnostic output, so that the Agent fixes issues without replaying the entire conversation.
9. As a developer, I want Review conducted as a fresh Agent invocation with only the diff and requirements, so that code is independently assessed rather than self-reviewed.
10. As a developer, I want review findings routed back to the implementation Worker, so that the Agent addresses them in the existing Workspace.
11. As a developer, I want gate, review, and CI retries to have separate configurable budgets, so that ordinary development churn doesn't exhaust a shared counter.
12. As a developer, I want every repair to rerun the full Quality Gate set, so that fixes don't silently break previously passing gates.
13. As a developer, I want Forge to commit validated work and create PRs automatically, so that I don't manage Git operations for agent-produced code.
14. As a developer, I want Forge to monitor CI checks after PR creation, so that CI failures are automatically routed back for repair.
15. As a developer, I want CI required checks determined by GitHub branch protection/rulesets, so that Forge respects the repository's existing merge policy without duplicating it.
16. As a developer, I want optional CI check failures ignored, so that non-blocking checks don't trigger unnecessary repair cycles.
17. As a developer, I want Issues that genuinely need human clarification to be labeled and commented with structured questions, so that I know exactly what information is missing.
18. As a developer, I want NEEDS_INFO Issues to release their Worker slot, so that other ready work continues while I answer.
19. As a developer, I want `forge resume` to detect new human comments on NEEDS_INFO Issues and resume work, so that I control when blocked work restarts.
20. As a developer, I want Dependencies declared in issue bodies with a canonical `## Dependencies` syntax, so that dependency metadata lives in the tracker alongside the work it describes.
21. As a developer, I want `.forge.yaml` config overrides for Dependencies, so that I have an escape hatch when issue body metadata is insufficient.
22. As a developer, I want Issues outside the Execution set loaded as External Issues when referenced as Dependencies, so that Forge tracks their merge state without requiring me to list every transitive dependency.
23. As a developer, I want External Issues observed but never executed, so that Forge doesn't take action on work I didn't ask it to do.
24. As a developer, I want dependency-blocked Issues to start from a base revision that includes their prerequisite's merged code, so that Workers branch from a lineage that actually contains the code they depend on.
25. As a developer, I want `forge init` to detect my repository's quality commands and generate `.forge.yaml`, so that initial setup doesn't require manual YAML authoring.
26. As a developer, I want `forge init` to use deterministic detection only, so that initialization is predictable and doesn't invoke an LLM.
27. As a developer, I want `forge status` to show current Issue states, Workers, Dependencies, PRs, and failures, so that I can monitor Execution progress at a glance.
28. As a developer, I want `forge cancel` to stop an Execution cleanly, so that I can abort work without corrupting state.
29. As a developer, I want `forge retry` to rerun a failed Issue, so that transient failures don't require a full re-execution.
30. As a developer, I want Execution state persisted transactionally in SQLite, so that an orchestrator crash doesn't require starting implementation over.
31. As a developer, I want `forge resume` to reconcile worktrees, branches, PRs, and CI state on restart, so that recovery is automatic.
32. As a developer, I want multiple Executions to run against the same repository concurrently, so that independent efforts aren't serialized.
33. As a developer, I want the same Issue prevented from being claimed by two Executions simultaneously, so that concurrent Executions don't produce conflicting implementations.
34. As a developer, I want branches scoped by Execution identity, so that concurrent Executions and reruns don't collide on branch names.
35. As a developer, I want telemetry capturing agent invocations, token usage, retry counts, gate runtimes, and cycle times, so that I can measure whether Forge actually reduces agent overhead.
36. As a developer, I want idempotent external operations, so that retries and restarts don't create duplicate PRs, labels, or comments.

## Implementation Decisions

### Language and storage

- Go for the orchestrator. It is infrastructure, not an ML application. See ADR 0001.
- SQLite for transactional state persistence. Storage abstracted behind an interface for future Postgres. See ADR 0002.
- Single statically-linked binary. CLI name: `forge`.

### Domain model

- Canonical vocabulary defined in `CONTEXT.md`. Key distinctions: Issue (not ticket), Worker (orchestrator's work unit), Agent (coding backend), Workspace (isolation boundary, not Git worktree), Repository Context vs. Execution Context.
- 16 Issue states with validated transitions: PENDING → BLOCKED_DEPENDENCY → READY → CLAIMED → PREPARING → IMPLEMENTING → VALIDATING → REVIEWING → COMMITTING → PR_CREATING → CI_PENDING → CI_FAILED → NEEDS_INFO → FAILED → DONE → CANCELLED.

### Dependency resolution

- Dependencies declared in issue body via canonical `## Dependencies` block with strict syntax. No freeform NLP parsing. See ADR 0003.
- Config overrides in `.forge.yaml` under `dependencies.overrides` take precedence over issue body.
- Dependencies form a DAG; cycles are detected and rejected before any work begins.
- A Dependency is satisfied only when the prerequisite's PR is merged into the applicable base branch. See ADR 0005.
- External Issues (dependencies outside the Execution set) are loaded as observed nodes — tracked but never executed. Closed does not equal satisfied. See ADR 0008.

### Base revision semantics

- The Execution records a starting base SHA for auditing.
- Each Worker captures its own start base when the Issue transitions to READY. A dependency-blocked Issue starts from a newer base containing prerequisite merged code. See ADR 0006.

### Agent invocation

- Agent interface: `Execute(ctx, AgentRequest) → AgentResult` with statuses IMPLEMENTED, NEEDS_INFO, FAILED.
- MVP backend: Claude Code only. Codex deferred to post-MVP. See wayfinder decision 03.
- The Agent receives normalized Execution Context: issue, acceptance criteria, dependency state, workspace path, workflow policy, repository context.
- The Agent does not perform Git operations, PR creation, label management, or workflow state decisions.

### Review

- Fresh second Agent invocation after Quality Gates pass. Same Workspace, no implementation conversation history. See ADR 0004.
- Reviewer receives: diff, issue requirements, repository policy, gate results.
- Returns APPROVED or CHANGES_REQUIRED with structured findings (severity, file, line, message).
- CHANGES_REQUIRED findings route back to the implementation Worker.

### Retry budgets

- Separate configurable ceilings for gate failures, review rejections, and CI failures. See ADR 0007.
- Default: gates 3, review 2, CI 3.
- Every repair reruns the full Quality Gate set regardless of trigger.

### Quality gates

- Configured in `.forge.yaml`, not discovered at runtime.
- Executed in order. Stop on first failure by default.
- Each gate records: name, command, start/end time, exit code, stdout, stderr.
- Agent feedback is bounded to configurable max output bytes.

### CI supervision

- Required checks determined by GitHub branch protection/rulesets via `GetMergeRequirements`. See wayfinder decision 05.
- Fallback: `ci.required_checks.mode: explicit` with a configured check list.
- Optional check failures do not trigger repair.
- CI failures route bounded diagnostics back to the Worker.
- Configurable poll interval for MVP.

### Needs-info flow

- Agent returns structured NEEDS_INFO with reason and questions.
- Forge adds configured label, posts structured comment, preserves Workspace, releases Worker slot.
- Resume is manual via `forge resume`. Forge re-fetches issue comments and detects new human input. See wayfinder decision 07.
- Resume provides: original issue context + previous question + new comments only.

### Concurrency

- Multiple Executions allowed against the same repository. See ADR 0009.
- Resource-scoped locks: Git metadata (short-lived), Issue (prevents dual claim), branch (publication).
- Branches include Execution identity: `forge/<execution-id>/<issue>`.
- Workspaces scoped: `.forge/worktrees/<execution-id>/<issue>/`.
- Same Issue in two active Executions disallowed by default.
- Configurable max parallel Workers per Execution.

### CLI

- `forge init` — deterministic repo-policy discovery, generates `.forge.yaml`. No LLM. See wayfinder decision 04.
- `forge execute <issues...> [--max-parallel N]` — start an Execution.
- `forge status [execution-id]` — show Execution state.
- `forge resume <execution-id>` — reconcile and resume.
- `forge retry <issue-execution-id>` — rerun a failed Issue.
- `forge cancel <execution-id>` — clean abort.

### Persistence

- SQLite schema: executions, execution_issues, dependencies, workers, workspaces, agent_runs, gate_runs, pull_requests, ci_runs, events.
- Every state transition creates an event.
- Transactional updates prevent duplicate claims.
- On restart: reconcile worktrees, branches, PRs, CI state.

### Configuration

- `.forge.yaml` at repository root.
- Sections: tracker, git (base, branch template, worktree root), execution (max parallel, retries), workflow, quality gates, pull requests, CI, blocked-issue behavior, agent provider, dependencies overrides.
- Explicit config wins over detection. Secrets never stored in config.

### Idempotency

- Before creating a PR, check if one exists for the branch.
- Before adding a label, check if already present.
- Worktree creation handles existing worktrees.
- Push handles existing remote branches.

### Telemetry

- Agent invocations, token usage (when backend exposes it), retry counts per category, gate runtimes, issue cycle time, CI repair attempts, context size.

## Testing Decisions

### What makes a good test

Tests validate external behavior through the public interface, not implementation details. A test should break only when the system's observable behavior changes, not when internals are refactored.

### Primary seam: Execution Engine integration tests

The primary testing seam is the Execution Engine — the orchestration core that coordinates the full pipeline. Tests at this level exercise: issue loading → DAG construction → scheduling → Worker dispatch → Agent invocation → Quality Gates → Review → commit → PR → CI monitoring.

Fake boundaries:
- **Agent interface** — fake adapter returns IMPLEMENTED, NEEDS_INFO, or FAILED deterministically based on test scenario configuration.
- **Tracker interface** — fake adapter with in-memory issues, labels, comments, and merge requirements.

Real components under test:
- SQLite with temporary databases.
- Git with temporary repositories and real worktrees.
- Gate Runner executing real subprocesses against test commands.

### Integration test scenarios

The fake Agent and Tracker adapters should support enough scenario configuration to exercise:

- Single Issue happy path (READY → DONE).
- Multi-Issue concurrent execution respecting max parallel.
- Dependency chain across multiple waves.
- External dependency blocking and satisfaction.
- Gate failure → retry → success.
- Review rejection → findings → repair → approval.
- CI failure → repair → green.
- NEEDS_INFO → resume with new comments → completion.
- Retry budget exhaustion (gate, review, CI independently).
- Cycle detection before work starts.
- Restart recovery with partially-completed Execution.
- Concurrent Execution with Issue-level lock conflict.
- Idempotent PR creation on retry.

### Secondary: unit tests for domain invariants

- State transition validation: legal transitions succeed, illegal transitions return explicit errors.
- DAG construction and cycle detection.
- Configuration parsing, defaults, and validation errors.
- Dependency parsing from issue body (canonical syntax only, freeform rejected).
- Retry budget accounting.
- Output bounding for agent feedback.

## Out of Scope

- Codex adapter (post-MVP; Agent interface must be correct enough that adding it requires no Scheduler changes).
- Stacked dependency branches (branching from prerequisite branches instead of merged base).
- GitLab, Linear, Jira tracker adapters.
- Container isolation or remote Workers.
- Graphical UI or workflow editor.
- Automatic PR merging.
- Daemon mode, webhook-driven needs-info resume, or `forge watch`.
- Wayfinder, to-spec, to-tickets automation integration.
- DSPy-optimized reasoning modules.
- Anthropomorphic role agents (PM, Engineer, QA).

## Further Notes

- The primary design rule: use LLM reasoning only where engineering judgment is required. Encode workflow mechanics and invariants in software.
- The name "forge" is intentionally replaceable and should not leak into internal architecture beyond the CLI entry point.
- 9 ADRs record the key architectural decisions. All are in `docs/adr/`.
- The wayfinder map (`.scratch/forge-mvp/map.md`) records the 10 design decisions that were resolved during specification. All decision tickets are closed.
- The revised ticket dependency graph (from IDEATION.md with Codex removed and `forge init` added as ticket 8A) should inform implementation ordering. The first end-to-end vertical slice is tickets 1, 2, 3, 4, 5, 6, 7, 8, 8A, 9, 10, 12, 13, 15, 16, 17, 18.
