# Forge

A deterministic orchestration layer for software-engineering agents. Forge owns workflow mechanics and invariants; coding agents own engineering judgment.

## Language

### Orchestration

**Execution**:
A user-requested orchestration run over one or more issues. The top-level unit of work. Records a starting base SHA for auditing, but individual Workers capture their own start base when they transition to READY — a dependency-blocked Issue starts from a newer base after its prerequisites merge.
_Avoid_: Run, session, job

**Issue**:
Forge's normalized representation of an issue-tracker item. All internal code operates on Issues, never on tracker-specific models.
_Avoid_: Ticket, task, item

**Dependency**:
A directed relationship indicating that one Issue must complete before another can begin. Dependencies form a DAG; cycles are errors. A Dependency is satisfied only when the prerequisite Issue's PR is merged into the applicable base — not when implementation is locally complete, and not when CI goes green.

**Dependency Source**:
Where a Dependency relationship originates. The canonical source is a `## Dependencies` block in the issue body; `.forge.yaml` overrides take precedence when present. The Scheduler consumes normalized Dependencies regardless of source.

**External Issue**:
An Issue referenced as a Dependency but not included in the Execution set. Loaded into the DAG as an observed node — tracked for satisfaction but never executed. Forge does not automatically add External Issues to the Execution. Satisfaction is checked by verifying merged code is reachable from the applicable base.
_Avoid_: Implicit dependency, phantom issue

**Scheduler**:
The component that computes ready work from the dependency DAG, current Issue states, and concurrency limits, then claims and dispatches Workers.

### Workers and agents

**Worker**:
One coding-agent invocation responsible for one Issue within an Execution. The orchestrator's unit of concurrent work.
_Avoid_: Agent (when referring to the orchestrator's work unit)

**Agent**:
The external coding backend (Claude Code, Codex) invoked by a Worker. The Agent receives normalized context and returns a structured result. It does not perform workflow mechanics.
_Avoid_: Worker (when referring to the coding backend), model, LLM

**Agent Adapter**:
The backend-specific implementation that translates between Forge's Agent interface and a particular coding backend's invocation protocol.

### Workspaces and context

**Workspace**:
An isolated environment associated with a single Issue execution. Currently implemented as a Git worktree, but the domain concept is the isolation boundary, not the Git mechanism.
_Avoid_: Worktree (in domain code; acceptable in the workspace manager implementation)

**Repository Context**:
Relatively stable information shared across all Workers in an Execution: quality gates, project structure, agent instructions, base revision. Compiled once per Execution.
_Avoid_: Context (alone — always qualify as "Repository Context" or "Execution Context")

**Execution Context**:
Information specific to one Worker: the normalized Issue, acceptance criteria, dependency state, Workspace path, branch, and workflow policy. Built per-Worker from Repository Context plus Issue-specific data.
_Avoid_: Context (alone)

### Quality and publication

**Quality Gate**:
A deterministic command required to pass before publication. Gates are configured, not discovered at runtime. Workers do not independently determine what gates to run.

**Gate Runner**:
The component that executes Quality Gates in order, captures results, and produces bounded failure feedback for the Agent.

**CI Supervisor**:
The component that monitors pull-request checks after publication. CI failures are routed back to the Worker with bounded diagnostics. Required checks are determined by the tracker's native merge requirements (GitHub branch protection/rulesets), not duplicated in Forge config.

**Merge Requirements**:
The set of conditions the target branch requires before a PR can merge. Queried from the Tracker Adapter, not configured in Forge. Optional check failures do not trigger CI repair.
_Avoid_: Required checks (as a Forge config concept — they belong to the tracker)

**Review**:
A fresh Agent invocation that evaluates implementation quality after Quality Gates pass. The Reviewer receives the diff, Issue requirements, and Repository Context — not the implementation Agent's prior conversation. Returns APPROVED or CHANGES_REQUIRED with structured findings routed back to the implementation Worker.
_Avoid_: Self-review, continuation review

**Retry Budget**:
Separate counters for gate failures, review rejections, and CI failures. Each has its own configurable ceiling. Every repair — whether from gate failure, review rejection, or CI failure — must rerun the full Quality Gate set before proceeding.

### Issue tracker

**Tracker Adapter**:
The normalized interface to an external issue tracker (GitHub, GitLab, etc.). Scheduler-facing code contains no tracker-specific models.

## Issue states

PENDING · BLOCKED_DEPENDENCY · READY · CLAIMED · PREPARING · IMPLEMENTING · VALIDATING · REVIEWING · COMMITTING · PR_CREATING · CI_PENDING · CI_FAILED · NEEDS_INFO · FAILED · DONE · CANCELLED
