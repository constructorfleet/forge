Agent Work Orchestrator

0. NAME: Forge

1. Summary

Build a deterministic orchestration layer for software-engineering agents.

The orchestrator owns:

* issue discovery and normalization
* dependency resolution
* ready-work scheduling
* isolated worktree creation
* repository policy discovery
* agent invocation
* deterministic quality gates
* commits
* pull-request creation
* CI monitoring
* retry/failure routing
* issue comments and labels
* execution state
* concurrency
* resumability

The coding agent owns:

* understanding the requested change
* exploring relevant source code
* choosing an implementation
* writing tests
* implementing code
* diagnosing implementation failures
* performing engineering review
* determining when requirements are ambiguous

The primary design rule is:

Use LLM reasoning only where engineering judgment is required. Encode workflow mechanics and invariants in software.

⸻

2. Problem

Current coding-agent workflows repeatedly spend context and tokens discovering deterministic facts and procedures.

Typical examples:

* discovering where issues live
* fetching issue comments
* determining dependencies
* deciding which issue can run next
* discovering repository quality commands
* discovering branching conventions
* creating worktrees
* remembering to run tests
* remembering to run formatting
* remembering to run linting
* remembering to run type checking
* remembering to run builds
* creating commits
* opening pull requests
* polling CI
* identifying failed checks
* commenting on blocked issues
* applying labels
* coordinating multiple workers

A command such as:

/goal Spawn team members to work through issues 345, 344, 343...
respect dependencies...
use worktrees...
use TDD...
run quality gates...
create PRs...
watch checks...
fix failures...

contains mostly workflow policy rather than engineering intent.

The desired interface is closer to:

execute 345 344 343 342 341 340 109

Everything else should come from repository and runtime configuration.

⸻

3. Goals

3.1 Primary goals

The system must:

1. Execute one or more issue-tracker tickets using coding agents.
2. Determine which tickets are actionable based on dependencies.
3. Execute independent tickets concurrently.
4. Give every worker an isolated Git worktree.
5. Start work from a configured base revision.
6. Compile repository and issue metadata before invoking the agent.
7. Invoke an agent with concise, normalized execution context.
8. Support TDD-oriented implementation policy.
9. Run deterministic repository quality gates.
10. Require quality gates to pass before publication.
11. Commit successful work.
12. Create pull requests.
13. Monitor CI checks.
14. Route CI failures back to the worker with focused diagnostics.
15. Mark issues needing human information appropriately.
16. Persist enough state to survive orchestrator restarts.
17. Resume incomplete executions safely.
18. expose execution state for humans and automation.

3.2 Secondary goals

The system should:

* support multiple coding-agent backends
* support multiple issue trackers eventually
* support repository-specific workflow policies
* eliminate duplicate context gathering between workers
* provide structured telemetry
* retain complete execution history
* allow individual workflow stages to be rerun
* allow manual intervention without corrupting state

⸻

4. Non-goals

The first version will not:

* implement its own LLM
* implement a general-purpose multi-agent conversation framework
* replace Git
* replace GitHub/GitLab
* replace CI
* autonomously invent product requirements
* automatically merge PRs
* automatically resolve merge conflicts between independent tickets
* infer arbitrary dependency relationships purely from source code
* provide a graphical workflow editor
* create anthropomorphic “PM”, “Engineer”, and “QA” agents

There is one implementation worker per active issue unless explicitly extended later.

⸻

5. Core concepts

Execution

A user-requested run over one or more tickets.

Example:

execute 345 344 343 342 341 340 109

Issue

Normalized representation of an issue-tracker ticket.

Dependency

A directed relationship indicating that one issue must complete before another can begin.

Worker

One coding-agent execution responsible for one issue.

Workspace

An isolated Git worktree associated with an issue execution.

Repository context

Relatively stable information shared across workers:

* repository instructions
* project structure
* package manager
* test commands
* lint commands
* formatting commands
* type-check commands
* build commands
* agent instructions
* contribution conventions

Execution context

Information specific to one worker:

* normalized issue
* acceptance criteria
* dependencies
* relevant issue comments
* repository context
* workspace path
* branch
* base revision
* workflow policy

Quality gate

A deterministic command or check required before publication.

⸻

6. Architecture

                         User / CLI
                             |
                             v
                    +------------------+
                    | Execution Engine |
                    +--------+---------+
                             |
              +--------------+--------------+
              |                             |
              v                             v
      +---------------+             +---------------+
      | Tracker       |             | Repo Context  |
      | Adapter       |             | Compiler      |
      +-------+-------+             +-------+-------+
              |                             |
              v                             |
       Issue graph / DAG                    |
              |                             |
              +-------------+---------------+
                            |
                            v
                    +---------------+
                    | Scheduler     |
                    +-------+-------+
                            |
                       Ready queue
                            |
             +--------------+---------------+
             |              |               |
             v              v               v
        +---------+    +---------+     +---------+
        | Worker  |    | Worker  |     | Worker  |
        +----+----+    +----+----+     +----+----+
             |              |               |
             v              v               v
         worktree       worktree        worktree
             |              |               |
             +--------------+---------------+
                            |
                            v
                    Coding Agent Adapter
                            |
                            v
                     source changes
                            |
                            v
                    +---------------+
                    | Gate Runner   |
                    +-------+-------+
                            |
                       pass / fail
                            |
                       +----+----+
                       |         |
                     fail       pass
                       |         |
                       v         v
                     agent     commit
                                 |
                                 v
                              PR create
                                 |
                                 v
                           CI Supervisor
                            /         \
                         failed      green
                           |           |
                           v           v
                         agent       complete

⸻

7. Important architectural boundary

The orchestrator must never ask the LLM questions that deterministic code can answer reliably.

Examples:

Orchestrator responsibility

Which issue is ready?
What issues block #342?
Where is the worktree?
What is the branch name?
Did lint pass?
Did CI finish?
Which CI check failed?
Should another ready issue be scheduled?
What GitHub label represents "needs information"?

Agent responsibility

What code should change?
What behavior should the test express?
What is the likely cause of this failure?
Does this requirement have multiple reasonable interpretations?
Is the implementation maintainable?
What existing abstraction should this feature use?

⸻

8. Technology decision

Implementation language

Use Go for the orchestrator.

Reasons:

* excellent process execution
* straightforward concurrency
* easy static distribution
* strong filesystem/process primitives
* simple long-running daemon behavior
* easy CLI construction
* predictable deployment
* excellent fit for state-machine orchestration
* avoids making the orchestrator depend on the Python environment of the repository being modified

Python remains perfectly capable, but the orchestrator is infrastructure rather than an ML application.

Storage

Use SQLite initially.

Requirements:

* transactional state transitions
* no external service required
* restart recovery
* queryable history
* sufficient concurrency for expected worker counts

Abstract storage behind an interface so Postgres can be added later.

CLI

One standalone executable.

Working name:

forge

The name is intentionally replaceable and should not leak into internal architecture.

⸻

9. CLI

Initial commands:

forge init
forge execute 345
forge execute 345 344 343
forge execute --max-parallel 4 345 344 343 342
forge status
forge status <execution-id>
forge issues
forge workers
forge resume <execution-id>
forge cancel <execution-id>
forge retry <issue-execution-id>

Useful later:

forge execute --all-ready
forge execute --milestone MP-82
forge execute --label agent-ready

⸻

10. Configuration

Repository configuration:

version: 1
tracker:
  type: github
git:
  base: origin/main
  branch_template: agent/{issue}
  worktree_root: .forge/worktrees
execution:
  max_parallel: 4
workflow:
  implementation: tdd
  review: true
quality:
  gates:
    - name: test
      command: make test
    - name: format
      command: make format-check
    - name: lint
      command: make lint
    - name: typecheck
      command: make typecheck
    - name: build
      command: make build
pull_requests:
  enabled: true
  watch_ci: true
blocked:
  label: needs-info
  comment: true
agent:
  provider: claude-code

Environment-specific configuration should remain outside the repository where appropriate.

Secrets must never be stored in this file.

⸻

11. Repository policy discovery

Explicit configuration wins.

When configuration is absent, forge init may discover candidate commands from:

* package.json
* Makefile
* Taskfile
* justfile
* pyproject.toml
* Cargo.toml
* go.mod
* CI workflow files
* AGENTS.md
* CLAUDE.md

Discovery is primarily an initialization concern.

Workers must not rediscover this independently.

Generated configuration must be reviewable before use.

⸻

12. Repository context compiler

Before workers start, compile one shared repository context.

Example:

{
  "base_revision": "abc123",
  "language": ["go", "typescript"],
  "package_managers": ["go", "pnpm"],
  "quality_gates": [
    "make test",
    "make format-check",
    "make lint",
    "make typecheck",
    "make build"
  ],
  "instructions": [
    "...normalized AGENTS.md rules..."
  ]
}

Repository context is immutable for a given execution unless explicitly refreshed.

This prevents worker A and worker B from independently spending tokens determining identical repository facts.

⸻

13. Tracker abstraction

Define a normalized interface:

type Tracker interface {
    GetIssue(ctx context.Context, id string) (*Issue, error)
    GetIssues(ctx context.Context, ids []string) ([]Issue, error)
    GetDependencies(ctx context.Context, id string) ([]Dependency, error)
    AddComment(ctx context.Context, id string, body string) error
    AddLabel(ctx context.Context, id string, label string) error
    RemoveLabel(ctx context.Context, id string, label string) error
}

Initial implementation:

GitHub

Future adapters may include:

GitLab
Linear
Jira

Do not expose GitHub-specific models to the scheduler.

⸻

14. Dependency model

Represent issue dependencies as a directed acyclic graph whenever possible.

345 ----\
         +--> 342 --> 340
344 ----/
343 --------> 341
109

The scheduler derives ready work:

Wave 1:
345
344
343
109
Wave 2:
342
341
Wave 3:
340

A cycle is a configuration/data error.

The orchestrator must detect cycles before starting work.

If tracker metadata cannot represent dependencies directly, allow repository configuration or issue metadata conventions to supply them.

⸻

15. Issue states

PENDING
BLOCKED_DEPENDENCY
READY
CLAIMED
PREPARING
IMPLEMENTING
VALIDATING
REVIEWING
COMMITTING
PR_CREATING
CI_PENDING
CI_FAILED
NEEDS_INFO
FAILED
DONE
CANCELLED

⸻

16. State transitions

PENDING
   |
   +-- unmet dependencies --> BLOCKED_DEPENDENCY
   |
   +-- dependencies done --> READY
                              |
                              v
                           CLAIMED
                              |
                              v
                          PREPARING
                              |
                              v
                        IMPLEMENTING
                         /         \
               needs info           implemented
                   |                    |
                   v                    v
              NEEDS_INFO           VALIDATING
                                    /       \
                                 fail       pass
                                  |          |
                                  v          v
                           IMPLEMENTING   REVIEWING
                                            |
                                            v
                                       COMMITTING
                                            |
                                            v
                                       PR_CREATING
                                            |
                                            v
                                        CI_PENDING
                                         /       \
                                      fail       pass
                                       |          |
                                       v          v
                                   CI_FAILED     DONE
                                       |
                                       v
                                IMPLEMENTING

All transitions must be persisted transactionally.

⸻

17. Workspace manager

Each issue receives an isolated Git worktree.

Example:

.forge/
  worktrees/
    345/
    344/
    343/

Branch:

agent/345
agent/344
agent/343

Creation semantics:

git worktree add <path> -b agent/345 origin/main

The base revision must be captured when the execution begins.

A worker must never modify the primary checkout.

⸻

18. Base revision and dependency semantics

Independent issues begin from the execution base revision.

Dependent issues require special handling.

If:

345 -> 342

and 342 logically requires code introduced by 345, then simply starting 342 from origin/main before 345 merges is incorrect.

Therefore the system supports dependency strategies.

Initial strategy:

merged

A dependent issue becomes executable only after prerequisite PRs are merged into the configured base branch.

Future strategy:

stacked

A dependent issue may branch from its prerequisite branch/commit.

Stacked execution is explicitly deferred because it introduces:

* stacked PR semantics
* rebase propagation
* dependency branch mutation
* changed CI ancestry
* complicated failure recovery

For MVP, dependency completion means merged, not merely PR-green.

⸻

19. Agent abstraction

type Agent interface {
    Execute(ctx context.Context, req AgentRequest) (AgentResult, error)
}

Request:

type AgentRequest struct {
    WorkspacePath string
    Issue         IssueContext
    Repository    RepositoryContext
    Workflow      WorkflowPolicy
    Feedback      []Feedback
}

Result:

type AgentResult struct {
    Status  AgentStatus
    Summary string
    NeedsInfo *NeedsInfoRequest
}

Statuses:

IMPLEMENTED
NEEDS_INFO
FAILED

Initial adapters:

claude-code
codex

The orchestrator should invoke agents through their supported noninteractive/automation interfaces rather than scrape terminal UI.

⸻

20. Agent prompt contract

Workers receive concise execution-oriented context.

Conceptually:

Implement issue #345.
Issue:
<normalized issue>
Acceptance criteria:
<criteria>
Dependencies:
All required dependencies are satisfied.
Repository:
<compiled repository context>
Workspace:
<path>
Workflow:
Use test-driven development.
Rules:
- Work only inside the provided workspace.
- Do not create or publish a PR.
- Do not manage issue labels.
- Do not decide workflow state.
- Stop and return NEEDS_INFO if implementation requires missing product or architectural information.
Return one status:
IMPLEMENTED
NEEDS_INFO
FAILED

The agent is deliberately not instructed how to perform GitHub operations because it does not perform them.

⸻

21. TDD policy

TDD remains an agent reasoning policy.

Required behavior:

1. identify externally observable behavior
2. add or modify a test that initially fails
3. implement the smallest useful change
4. make the test pass
5. refactor as appropriate
6. run relevant tests

The orchestrator does not attempt to mechanically verify red-green-refactor ordering.

It verifies final repository quality.

⸻

22. Quality-gate runner

Quality gates are deterministic subprocesses.

Each gate records:

name
command
start time
end time
exit status
stdout
stderr

Example:

test       PASS
format     PASS
lint       FAIL
typecheck  NOT RUN
build      NOT RUN

On failure, subsequent gates may stop by default.

The agent receives focused feedback:

Quality gate failed:
Gate:
lint
Command:
make lint
Exit code:
1
Output:
<bounded relevant output>

The agent is then resumed inside the same workspace.

Do not send the entire previous conversation back merely because ESLint discovered a semicolon.

⸻

23. Output bounding

Potentially enormous command output must be bounded.

Store complete logs on disk/database where practical.

Agent feedback should include:

* failing command
* exit code
* relevant beginning/end
* matched error sections

Configurable maximum:

agent_feedback:
  max_output_bytes: 20000

⸻

24. Review stage

After gates pass, run a separate review operation.

The implementation agent may perform this itself initially.

Review policy examines:

* correctness
* requirements coverage
* maintainability
* unnecessary complexity
* repository conventions
* suspicious missing tests
* accidental unrelated changes

Review returns:

APPROVED
or
CHANGES_REQUIRED
<findings>

CHANGES_REQUIRED routes findings back into implementation.

A future version may use a dedicated reviewer model/backend.

⸻

25. Commit handling

The orchestrator owns the final commit operation.

Before committing:

git status
git diff
quality gates passed
review approved

Commit message default:

<issue title>
Refs #345

Repository-specific templates may override this.

The agent does not need to spend tokens debating Git syntax.

⸻

26. Pull requests

After commit:

1. push branch
2. create PR
3. include issue reference
4. record PR ID and URL
5. transition to CI_PENDING

Default PR body contains:

Summary
<agent implementation summary>
Validation
- [x] tests
- [x] formatting
- [x] lint
- [x] typecheck
- [x] build
Issue
Closes #345

⸻

27. CI supervisor

The orchestrator polls or subscribes to PR status.

MVP may poll.

States:

PENDING
PASS
FAIL

On failure:

1. identify failing check
2. fetch bounded failure details
3. transition issue to CI_FAILED
4. invoke worker with CI feedback
5. rerun local gates
6. commit correction
7. push
8. return to CI_PENDING

Configurable retry limit:

ci:
  max_fix_attempts: 3

Exhaustion transitions to:

FAILED

with diagnostics preserved.

⸻

28. Needs-information flow

The agent may return:

{
  "status": "NEEDS_INFO",
  "reason": "Behavior when X occurs is unspecified",
  "questions": [
    "Should X return 404 or an empty response?"
  ]
}

The orchestrator:

1. adds configured label
2. posts a structured issue comment
3. transitions to NEEDS_INFO
4. releases worker capacity
5. preserves workspace

Example comment:

Implementation is blocked pending clarification.
Question:
Should X return 404 or an empty response?
Relevant context:
<brief explanation>

No speculative implementation proceeds.

⸻

29. Scheduler

Scheduler inputs:

execution issue set
dependency DAG
current states
max concurrency

Algorithm:

1. Load all issue states.
2. Mark issues with unmet dependencies BLOCKED_DEPENDENCY.
3. Mark eligible issues READY.
4. While worker slots are available:
   a. select READY issue
   b. atomically transition READY -> CLAIMED
   c. launch worker
5. On meaningful state change, recompute eligibility.

The scheduler must prevent duplicate claims.

⸻

30. Concurrency

Default:

execution:
  max_parallel: 4

Concurrency applies to implementation workers.

Cheap deterministic tasks do not consume worker capacity.

Examples:

issue fetching
dependency resolution
CI polling
state persistence

⸻

31. Persistence

SQLite schema conceptually contains:

repositories
executions
execution_issues
dependencies
workers
workspaces
agent_runs
gate_runs
pull_requests
ci_runs
events

Every major state change creates an event.

Example:

2026-08-28T13:00 READY          issue=345
2026-08-28T13:01 CLAIMED        issue=345
2026-08-28T13:01 IMPLEMENTING   issue=345
2026-08-28T13:18 VALIDATING     issue=345
2026-08-28T13:21 REVIEWING      issue=345
2026-08-28T13:25 PR_CREATING    issue=345
2026-08-28T13:26 CI_PENDING     issue=345

⸻

32. Restart recovery

On startup:

1. load incomplete executions
2. validate worktrees
3. inspect process ownership
4. reconcile issue states
5. reconcile open PR states
6. resume CI monitoring
7. mark orphaned active workers recoverable
8. allow explicit forge resume

An orchestrator crash must not require starting implementation over from scratch.

⸻

33. Idempotency

Operations that interact with external systems must be idempotent where possible.

Examples:

create worktree
push branch
create PR
add label
post status comment

Before creating a PR, query whether one already exists for the branch.

Before adding a label, determine whether it is already present.

⸻

34. Context efficiency

The orchestrator explicitly optimizes agent context.

Shared once per execution

* repository policy
* project commands
* base revision
* repository instructions

Per issue

* issue body
* relevant comments
* acceptance criteria
* dependency information

Per retry

Only new information:

* quality failure
* review finding
* CI failure
* human clarification

Avoid replaying historical orchestration prose.

⸻

35. Skills integration

Skills such as:

wayfinder
to-spec
to-tickets
implement
tdd
code-review

should be classified into three categories.

Reasoning policies

Remain model instructions.

Examples:

TDD methodology
architectural review
Wayfinder decision selection

Workflow policies

Become declarative configuration.

Examples:

TDD -> review -> gates -> commit -> PR -> CI

Mechanics

Become implementation.

Examples:

issue fetching
worktree creation
branch naming
quality execution
PR creation
CI monitoring
labels
comments

⸻

36. Future Wayfinder mode

The same runtime can later support decision-oriented work.

Example:

forge wayfind 123

The scheduler owns:

* map issue discovery
* child-ticket discovery
* frontier calculation
* claiming
* comments
* closing decision tickets

The agent owns:

* examining the uncertainty
* researching the repository
* identifying alternatives
* making or requesting the decision
* recording rationale

This is deliberately outside MVP implementation execution.

⸻

37. Observability

Human-readable:

$ forge status
Execution e-123
Base: origin/main@abc123
ISSUE  STATE                WORKER  PR
345    CI_PENDING           -       #812
344    IMPLEMENTING         w-3     -
343    DONE                 -       #809
342    BLOCKED_DEPENDENCY   -       -
341    BLOCKED_DEPENDENCY   -       -
340    BLOCKED_DEPENDENCY   -       -
109    NEEDS_INFO           -       -

Structured logs should include:

execution_id
issue_id
worker_id
state
event
duration
agent backend
token usage when available

Token accounting is particularly valuable because reducing orchestration waste is an explicit project goal.

⸻

38. Security

The agent already receives substantial repository access, so magical sandbox pixie dust should not be assumed.

Minimum controls:

* explicit workspace boundary
* sanitized environment
* configurable inherited environment variables
* secrets excluded from model context
* external credentials provided only when required
* command logs
* no arbitrary orchestrator DB access from workers
* no issue-tracker mutation credentials required by worker agent when avoidable

Future versions may add containerized workers.

⸻

39. Success criteria

An execution over multiple dependent tickets should require only:

forge execute 345 344 343 342 341 340 109

The system must then:

* discover issue contents
* construct dependency state
* start independent work concurrently
* isolate every worker
* supply normalized context
* invoke the configured coding agent
* run configured gates
* route failures back to workers
* review changes
* commit successful work
* open PRs
* watch CI
* address CI failures
* identify information-blocked issues
* label/comment those issues
* update dependent work when prerequisites become eligible
* persist all state

No repeated natural-language description of the workflow should be necessary.

⸻

40. MVP boundary

The first usable version ends at:

GitHub
+
local Git repository
+
Git worktrees
+
Claude Code adapter
+
Codex adapter
+
static dependency metadata
+
configurable quality gates
+
PR creation
+
CI polling
+
SQLite state

Explicitly defer:

GitLab
Linear
Jira
stacked PRs
container isolation
remote workers
distributed scheduler
graphical UI
automatic merging
DSPy optimization
Wayfinder automation
to-spec automation
to-tickets automation

The vertical slice matters more than accumulating adapter interfaces for systems nobody has asked it to use.

⸻

Actionable tickets

Ticket 1: Establish project skeleton and core domain model

Goal

Create the Go application and define implementation-independent domain types.

Implement

* Go module
* CLI executable
* package structure
* execution model
* issue model
* dependency model
* worker model
* workspace model
* state enum
* transition validation

Acceptance criteria

* project builds
* state transitions have unit tests
* illegal transitions return explicit errors
* domain package has no GitHub, Git, Claude, or SQLite dependencies

Dependencies

None.

⸻

Ticket 2: Add configuration loading and validation

Goal

Load .forge.yaml repository configuration.

Implement

* YAML schema
* defaults
* validation
* quality-gate definitions
* Git configuration
* execution configuration
* agent selection
* blocked-ticket behavior
* CI retry policy

Acceptance criteria

* valid configuration loads
* useful errors identify invalid fields
* defaults are deterministic
* tests cover malformed and partial configurations

Dependencies

Ticket 1.

⸻

Ticket 3: Implement SQLite persistence

Goal

Persist execution and issue state.

Implement

* schema migrations
* executions
* execution issues
* dependencies
* state transitions
* event log
* transactional updates

Acceptance criteria

* execution can be created and reloaded
* state transitions survive restart
* duplicate issue claims are prevented transactionally
* migration tests pass

Dependencies

Ticket 1.

⸻

Ticket 4: Implement GitHub tracker adapter

Goal

Normalize GitHub issues into internal issue models.

Implement

* fetch issue
* fetch multiple issues
* comments
* labels
* add comment
* add label
* remove label
* authentication/configuration

Acceptance criteria

* scheduler-facing code contains no GitHub-specific models
* integration tests use a mocked HTTP/API boundary
* rate-limit errors surface clearly

Dependencies

Tickets 1 and 2.

⸻

Ticket 5: Define dependency input and DAG validation

Goal

Build and validate the execution dependency graph.

Implement

* dependency representation
* configured/static dependency input
* graph construction
* cycle detection
* prerequisite lookup
* dependent lookup

Acceptance criteria

Given:

345 -> 342
344 -> 342
342 -> 340
343 -> 341
109

the graph returns:

ready: 345,344,343,109
blocked: 342,341,340

Cycles produce a useful error before workers launch.

Dependencies

Tickets 1 and 4.

⸻

Ticket 6: Implement scheduler

Goal

Convert issue state and dependency state into concurrent work.

Implement

* READY calculation
* dependency blocking
* atomic claiming
* max-parallel handling
* worker lifecycle hooks
* scheduler wakeup on state changes

Acceptance criteria

* independent issues execute concurrently
* dependent issues never execute early
* worker concurrency never exceeds configuration
* duplicate scheduling does not occur
* scheduler tests use deterministic fake workers

Dependencies

Tickets 3 and 5.

⸻

Ticket 7: Implement Git workspace manager

Goal

Create and manage isolated worktrees.

Implement

* capture base revision
* branch naming
* worktree creation
* worktree validation
* cleanup
* recovery inspection

Acceptance criteria

* each issue receives a unique branch/worktree
* worktrees start from captured base revision
* primary checkout is never modified
* existing worktrees are handled idempotently
* Git failures produce actionable errors

Dependencies

Tickets 1 and 2.

⸻

Ticket 8: Implement repository context compiler

Goal

Compile stable repository instructions once per execution.

Implement

Read and normalize relevant:

* .forge.yaml
* AGENTS.md
* CLAUDE.md
* project manifest metadata
* configured quality gates
* base revision

Acceptance criteria

* repository context is generated once
* workers consume persisted/compiled context
* worker invocation does not independently rediscover quality commands
* tests cover nested agent instructions where applicable

Dependencies

Tickets 2 and 7.

⸻

Ticket 9: Define coding-agent adapter contract and fake adapter

Goal

Create backend-independent agent execution.

Implement

* Agent interface
* AgentRequest
* AgentResult
* statuses
* structured needs-information result
* fake deterministic adapter for tests

Acceptance criteria

* orchestrator code does not depend on Claude/Codex command syntax
* fake agent supports success, failure, and needs-info scenarios
* scheduler integration tests use fake adapter

Dependencies

Tickets 1 and 8.

⸻

Ticket 10: Implement Claude Code adapter

Goal

Run Claude Code in a worker worktree with normalized context.

Implement

* process invocation
* prompt construction
* working-directory enforcement
* structured result parsing
* stdout/stderr capture
* cancellation
* exit-code handling

Acceptance criteria

* Claude executes only from supplied workspace
* issue and repository context are provided
* workflow mechanics are not unnecessarily repeated in prompt
* IMPLEMENTED, NEEDS_INFO, and FAILED are distinguishable

Dependencies

Ticket 9.

⸻

Ticket 11: Implement Codex adapter

Goal

Provide equivalent worker execution through Codex.

Acceptance criteria

Same behavioral contract as Claude adapter.

Backend selection must require no scheduler changes.

Dependencies

Ticket 9.

⸻

Ticket 12: Implement deterministic quality-gate runner

Goal

Run configured validation commands after implementation.

Implement

* ordered commands
* timeout support
* output capture
* bounded feedback
* persistence
* stop-on-failure behavior

Acceptance criteria

* passing gates transition workflow forward
* failing gate records complete result
* agent receives concise failure context
* retries happen in the existing workspace

Dependencies

Tickets 2, 3, and 9.

⸻

Ticket 13: Add implementation retry loop

Goal

Route gate failures back to the coding worker.

Flow

agent
  ->
quality gates
  ->
failure
  ->
agent + new failure only
  ->
quality gates

Acceptance criteria

* workspace is preserved
* agent receives new diagnostic information
* execution history is persisted
* configurable retry ceiling exists
* retry exhaustion results in FAILED

Dependencies

Tickets 10 or 11, plus Ticket 12.

⸻

Ticket 14: Implement needs-information workflow

Goal

Handle genuinely underspecified tickets without speculative implementation.

Implement

On structured NEEDS_INFO:

* add configured label
* post structured comment
* persist questions/reason
* transition state
* release worker slot

Acceptance criteria

* no PR is created
* workspace remains available
* issue receives exactly one idempotent status update
* other ready work continues

Dependencies

Tickets 4, 6, and 9.

⸻

Ticket 15: Add code-review stage

Goal

Review implementation after quality gates pass.

Implement

* review request contract
* review policy prompt
* APPROVED result
* CHANGES_REQUIRED result
* findings routed to worker
* review retry ceiling

Acceptance criteria

* commit cannot occur before review approval
* findings return to existing worker context/workspace
* review operations are recorded

Dependencies

Tickets 12 and 13.

⸻

Ticket 16: Implement commit and push manager

Goal

Publish validated changes to a remote branch.

Implement

* inspect dirty state
* commit
* configurable commit template
* push branch
* capture commit SHA
* idempotent recovery

Acceptance criteria

* only approved/gate-passing work is committed
* rerunning publication does not create duplicate commits unnecessarily
* resulting branch exists remotely

Dependencies

Tickets 7 and 15.

⸻

Ticket 17: Implement GitHub pull-request publisher

Goal

Create or recover a PR for completed implementation.

Implement

* detect existing PR
* create PR
* title/body generation
* issue linking
* persist PR identity

Acceptance criteria

* exactly one PR exists per issue execution
* retries recover existing PR
* PR contains validation summary
* execution transitions to CI_PENDING

Dependencies

Tickets 4 and 16.

⸻

Ticket 18: Implement CI status supervisor

Goal

Monitor PR checks until completion.

Implement

* poll GitHub checks
* aggregate state
* configurable poll interval
* persist CI attempts
* terminal green state
* terminal failure state

Acceptance criteria

* pending checks do not block scheduler progress
* all required green checks produce success
* failed required checks produce CI_FAILED
* orchestrator restart resumes monitoring

Dependencies

Tickets 3 and 17.

⸻

Ticket 19: Route CI failures back to worker

Goal

Automatically repair implementation-related CI failures.

Implement

* retrieve failing check details
* bound diagnostic output
* resume existing workspace
* invoke agent with CI feedback
* rerun local gates
* review
* commit
* push
* resume CI supervision

Acceptance criteria

* agent is not given unrelated historical context
* updated commits appear on existing PR
* retry limit prevents infinite repair loops
* successful repair returns to CI_PENDING

Dependencies

Tickets 13, 15, 16, and 18.

⸻

Ticket 20: Implement execution resume/recovery

Goal

Recover safely after process termination.

Implement

Reconcile:

* active executions
* claimed issues
* worktrees
* remote branches
* PRs
* pending CI checks

Acceptance criteria

Killing the orchestrator during:

implementation
validation
PR creation
CI wait

does not require recreating the execution.

Dependencies

Tickets 3, 7, 17, and 18.

⸻

Ticket 21: Implement operational CLI

Goal

Provide useful execution controls.

Commands

forge execute
forge status
forge resume
forge retry
forge cancel

Acceptance criteria

forge status clearly exposes:

* current state
* dependencies
* workers
* PRs
* failures

Dependencies

Tickets 6, 17, 18, and 20.

⸻

Ticket 22: Add execution telemetry and token accounting

Goal

Measure whether the project actually reduces agent overhead rather than merely moving complexity somewhere more fashionable.

Capture

* agent runtime
* agent invocations
* retries
* token usage where backend exposes it
* gate runtime
* issue cycle time
* CI repair attempts
* context size

Acceptance criteria

An execution can report approximately:

issues completed:       7
agent invocations:      11
input tokens:           184,221
output tokens:           31,410
gate retries:             2
CI retries:               1
wall-clock duration:     ...

Dependencies

Tickets 9, 12, and 18.

⸻

MVP dependency graph

1
├── 2
│   ├── 4
│   │   ├── 5
│   │   │   └── 6
│   │   └── 14
│   └── 7
│       └── 8
│           └── 9
│               ├── 10
│               └── 11
├── 3
│   ├── 6
│   ├── 12
│   └── 18
│
9 + 12
   └── 13
       └── 15
           └── 16
               └── 17
                   └── 18
                       └── 19
3 + 7 + 17 + 18
   └── 20
6 + 17 + 18 + 20
   └── 21
9 + 12 + 18
   └── 22

⸻

Suggested execution waves

Foundation

1

then:

2
3

External/domain infrastructure

4
7

then:

5
8

then:

6
9

Agent execution

Parallel:

10
11
12

then:

13
14

Completion pipeline

15
16
17
18
19

Production usability

20
21
22

⸻

First end-to-end milestone

Do not wait for all 22 tickets before proving the architecture.

The first meaningful vertical slice should be:

1  Domain model
2  Config
3  SQLite
4  GitHub issues
5  Dependency DAG
6  Scheduler
7  Worktrees
8  Repo context
9  Agent abstraction
10 Claude adapter
12 Gate runner
13 Retry loop
16 Commit/push
17 PR creation
18 CI monitoring

That should enable:

forge execute 345 344

and autonomously reach:

Issue -> worktree -> Claude -> gates -> commit -> PR -> CI green

Everything after that improves robustness rather than proving the core architecture.

⸻

Deferred roadmap

Once the execution runtime proves useful:

Phase 2

* automated /wayfinder
* /to-spec
* /to-tickets
* execution plans generated directly into tracker tickets
* richer dependency metadata

Phase 3

* stacked dependency branches
* GitLab adapter
* Linear adapter
* remote workers
* containerized execution

Phase 4

* evaluation corpus
* DSPy-optimized reasoning modules
* historical review-quality measurement
* backend/model selection by task type


