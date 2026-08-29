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
A directed relationship indicating that one Issue must complete before another can begin. Dependencies form a DAG; cycles are errors. A Dependency is satisfied only when the prerequisite Issue's PR is merged into the applicable base — not when implementation is locally complete, and not when CI goes green. A Dependency governs both *when* the dependent Issue may run and *what repository state* it runs against: a dependent with one or more Dependencies within the Execution set starts from its Dependencies' resulting branches (a single Dependency's branch directly, or an Integration Branch when there is more than one), not the Execution's original base branch.

**Integration Branch**:
A synthetic branch (`forge/integration/<issue>`) built by merging several Dependencies' branches together for an Issue with more than one Dependency, so that Issue's Worker starts from a repository state containing all of them. Recomputed from scratch each time it's needed; a merge conflict between Dependencies aborts and surfaces deterministically rather than dropping one side.

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

### Semantic navigation

**Semantic Navigation**:
Language-aware code exploration made available to an Agent — definitions, references, implementations, hover/type/signature, document and workspace symbols, and where available call and type hierarchy. Delivered capability-first: the harness's own tooling is used where present, and Forge supplies only the gap. Every result resolves to a source location the Agent can read.
_Avoid_: LSP (as the domain concept — LSP is one implementation mechanism, not the capability)

**Semantic Capabilities**:
The set of semantic-navigation operations a backend exposes natively, recorded one flag per operation. Describes what the harness itself can already do, independent of any repository or language.

**Injection Channel**:
The mechanism by which Forge adds semantic navigation to a backend that lacks it — a Model Context Protocol server, a language-server plugin the harness loads, or none. A property of the backend, not of the language or repository.

**Semantic Profile**:
A backend's declared pairing of its Semantic Capabilities with its Injection Channel — the single fact the component fulfilling Semantic Navigation reads to decide, per capability, whether to rely on the harness or fill the gap. A backend that declares no profile receives no Semantic Navigation (a safe, inert default) rather than a broken one.

**Language Server**:
A language-specific server process (e.g. gopls for Go) that Forge starts against a Workspace to supply Semantic Navigation the harness lacks — the concrete backing of a Forge-managed Injection Channel. Distinct from the harness's own native tooling, and never run alongside a native server for the same language in one Workspace.

**Language Server Registry**:
The mapping from a detected language to the Language Server that serves it (Go → gopls). Seeded with built-in defaults and extended or overridden by configuration; detection gates which entries actually start, configuration supplies their command.

**Semantic Provider**:
The component that fulfils Semantic Navigation for a single agent invocation. Reading a backend's Semantic Profile and the worktree's detected Language Servers, it decides per capability whether to rely on the harness or fill the gap, owns the lifecycle of any Forge-managed Language Server, and is best-effort — a provisioning failure degrades to no Semantic Navigation rather than failing the work. Path-agnostic: any agent working in a filesystem context is a potential caller, though only execution Workers are served today.

**Source Location**:
A normalized reference to a point in the source — a file path and line (optionally column and end position) — that an Agent can hand directly to a file read. The common output currency of Semantic Navigation: every location-returning capability resolves to one, so results flow straight into the Agent's normal reading regardless of which provider produced them.

## Issue states

PENDING · BLOCKED_DEPENDENCY · READY · CLAIMED · PREPARING · IMPLEMENTING · VALIDATING · REVIEWING · COMMITTING · PR_CREATING · CI_PENDING · CI_FAILED · NEEDS_INFO · NEEDS_REPLAN · FAILED · DONE · CANCELLED

## Replan

**Replan**:
The conservative response to a Worker discovering that the plan governing its Issue is itself invalid. The Agent returns `REPLAN_REQUIRED` with a structured reason, evidence, affected requirements, and a suggested planning question; the reporting Issue enters **NEEDS_REPLAN**, whose only exits are back to READY or to CANCELLED.

**Feature freeze**:
On a replan, Forge freezes the Feature's *scheduling and integration* before it acquires the Feature planning lease. A frozen Feature admits no new Workers, and any Worker already in flight may finish to its safe suspension boundary (commit and push its own branch) but is refused at the step that would integrate against the invalidated plan — it is parked in NEEDS_REPLAN rather than killed. The freeze precedes the lease so a Feature is never left dispatching work against a plan already known to be invalid.

**Replan Decision**:
The trigger is materialized as a created — or, for a repeat escalation from the same Issue, reopened — Decision. A reopened Decision keeps its prior reasoning, records the trigger, and drops its approval; its content revision therefore moves, and every downstream artifact evaluates STALE purely by provenance comparison. There is no stored staleness bit anywhere.

**Implemented facts**:
Completed, merged work enters the PlanningContext as `implemented_facts[]`, each carrying the *old* ticket plan revision it was completed under. Completed work is fact: it is never auto-rolled-back, and a new plan is written around it. Work that merely finished mid-replan is parked in NEEDS_REPLAN, not DONE, so it never becomes a fact and never counts toward planning readiness.

**Superseded**:
Once a new plan is approved, the unstarted Issues it no longer contains are closed as superseded (CANCELLED, with an `issue.superseded` Event naming the superseding plan revision) — never recycled. Only then is the freeze lifted, so a new plan approval is genuinely required before frozen work resumes; each resumed result is revalidated rather than trusted for having merely finished.
