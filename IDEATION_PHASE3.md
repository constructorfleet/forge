Phase 3 is qualitatively different from Phase 2. Phase 1 made execution deterministic, Phase 2 made planning deterministic around the LLM, and Phase 3 is where Forge stops assuming “one local GitHub checkout on this machine” is the universe. Humanity has once again discovered distributed systems, presumably because local processes weren’t causing enough suffering.

I’d define the phase around one architectural goal:

Make Forge’s execution model independent of tracker, SCM host, workspace topology, and execution location.

Your five items actually collapse into three architectural workstreams:

1. SCM/tracker abstraction: GitLab + Linear
2. dependency-aware workspace topology: stacked dependency branches
3. execution substrate abstraction: remote workers + containers

And I would explicitly order them that way internally because remote/container execution will expose every accidental local-filesystem assumption you currently have.

Phase 3: Distributed / Portable Execution

The target architecture becomes:

                     Forge Controller
                            │
             ┌──────────────┼──────────────┐
             │              │              │
             ▼              ▼              ▼
        Tracker API       SCM API      Execution Backend
             │              │              │
       ┌─────┴─────┐   ┌────┴────┐   ┌─────┴──────────────┐
       │           │   │         │   │                    │
     GitHub      Linear GitHub  GitLab Local          Remote
       │           │   │         │   │                    │
       └───────────┘   └─────────┘   │             ┌──────┴──────┐
                                     │             │             │
                                     ▼             ▼             ▼
                                  host          container      worker

The important correction here is that GitLab and Linear are not equivalent adapters.

GitLab can be both:

tracker
+
SCM / PR host
+
CI provider

Linear is basically:

tracker

with GitHub/GitLab still owning branches, PRs/MRs, and CI.

So don’t create a giant Provider interface. That way lies an interface containing 47 optional methods and a comment reading “not supported for Linear,” which is software engineering’s version of mildew.

Instead split capabilities.

type Tracker interface {
    GetIssue(...)
    ListIssues(...)
    CreateIssue(...)
    UpdateIssue(...)
    AddComment(...)
    SetState(...)
    GetDependencies(...)
}
type SCM interface {
    GetRepository(...)
    CreateBranch(...)
    PushBranch(...)
    GetMergeBase(...)
    CreateChangeRequest(...)
    GetChangeRequest(...)
}
type CI interface {
    RequiredChecks(...)
    CheckStatus(...)
    FailureDiagnostics(...)
}

Then configurations can compose providers:

tracker:
  type: linear
scm:
  type: github
ci:
  type: github

or:

tracker:
  type: gitlab
scm:
  type: gitlab
ci:
  type: gitlab

That needs to become a frozen Phase 3 principle early.

⸻

1. Stacked dependency branches

This is probably the most subtle part of Phase 3.

Your existing Phase 1 model says:

345 READY at base A
345 merges
342 READY at newer base B containing 345

That is safe, but serializes execution whenever one ticket logically depends on another.

Stacked branches let you execute:

345
 │
 ▼
342
 │
 ▼
340

without waiting for each predecessor to merge.

The topology becomes:

origin/main
    │
    └── forge/345
           │
           └── forge/342
                  │
                  └── forge/340

So:

345 starts from origin/main
342 starts from forge/345
340 starts from forge/342

That sounds straightforward until 345 changes after review, gets rebased, or gets squashed on merge. Then the graph starts throwing furniture.

The critical distinction

Forge must separate:

logical dependency

from:

branch ancestry

They are related but not identical.

Each Issue execution should capture something like:

ExecutionBase
  source_type
  source_ref
  source_commit
  logical_dependencies[]

For ordinary work:

source_type = BASE_BRANCH
source_ref = origin/main

For stacked work:

source_type = ISSUE_BRANCH
source_issue = 345
source_ref = forge/345
source_commit = abc123

The commit matters.

Never merely record:

base = forge/345

because that ref can move.

⸻

Stacked execution state

I’d introduce:

WAITING_FOR_DEPENDENCY_CODE
READY_STACKED
READY_BASE

or better, avoid bloating IssueState and derive execution eligibility:

dependency_state:
  logical_satisfied: bool
  code_available: bool
  merged: bool

An issue can execute when dependency code is either:

merged into applicable base
OR
available through a Forge-managed dependency branch

But it can only reach final DONE under whatever merge semantics you define.

⸻

Merge ordering

Stacked PRs/MRs must merge in dependency order.

For:

345
 ↓
342
 ↓
340

Forge may create all three change requests, but:

342 cannot merge before 345
340 cannot merge before 342

Even if GitHub/GitLab technically allows it.

That is deterministic scheduler policy.

⸻

Rebasing after dependency merge

Suppose:

forge/342
  based on forge/345@abc

and 345 merges as:

main@xyz

perhaps squashed.

Forge should rebase or reconstruct 342 onto the updated base branch.

Conceptually:

old:
main ─ A
       \
        B C        # 345
           \
            D E    # 342
after 345 squash merge:
main ─ A ─ S
           \
            D' E'

Forge owns this mechanical transformation.

The model should only become involved on a semantic conflict, not routine rebasing.

⸻

Stacked branch MVP

I would constrain MVP significantly:

* Forge-managed branches only
* single-parent dependency chains initially
* stacked execution permitted only when an issue has exactly one active code dependency
* dependency must itself be executed by the same Forge repository/project context
* Forge rebases child after parent merges
* merge conflicts go to agent repair
* child PR target can remain base branch if diff calculation is normalized, or target parent branch depending on host semantics

Then later support:

A ─┐
   ├─> C
B ─┘

because multi-parent branch ancestry requires synthesizing a base containing both A and B.

That means temporary integration branches or merge commits, and suddenly your “simple optimization” has become Git graduate school.

Do not make that MVP.

⸻

2. GitLab adapter

GitLab should be used to prove your abstractions are real rather than GitHubWhatever interfaces wearing fake mustaches.

Forge needs capability-specific adapters for:

GitLabTracker
GitLabSCM
GitLabCI

Common domain concepts should be normalized:

Issue
Comment
Label
ChangeRequest
Check
RequiredCheck
Branch
Commit
Repository

Avoid leaking:

PullRequest
CheckRun
GitHubIssue

into orchestration packages.

Use:

ChangeRequest
PipelineCheck
TrackerIssue

or equivalent neutral terminology.

GitLab semantic mismatches to explicitly handle

GitLab gives you:

Merge Requests
pipelines/jobs
issue links
labels
approval rules
protected branches

These are similar to GitHub, not identical.

Forge should normalize only semantics it actually consumes.

For example:

type MergeRequirement struct {
    Name     string
    Required bool
    Status   RequirementStatus
}

rather than trying to make GitLab pipelines pretend to be GitHub check runs internally.

The scheduler wants:

can this change merge?
if not, why?
is the failure repairable?

It should not care whether that came from a workflow run, pipeline job, sacrificial goat, or Jenkins.

⸻

3. Linear adapter

Linear is where the separation between tracker and SCM becomes mandatory.

Configuration should permit:

tracker:
  type: linear
  team: FORGE
scm:
  type: github
ci:
  type: github

Phase 1 and Phase 2 should consume normalized issue IDs internally.

Something like:

IssueRef {
    provider: "linear"
    external_id: "abc..."
    display_id: "FOR-345"
}

Never assume issue IDs are integers.

If Forge currently has int issueID scattered around the codebase, Phase 3 gets the delightful privilege of discovering all of them.

⸻

Dependency representation

Your frozen canonical GitHub format is currently:

## Dependencies
Depends on:
- #123

That cannot remain the universal domain model.

It can remain the GitHub adapter encoding.

Forge’s internal representation must become:

DependencyEdge {
    from IssueRef
    to IssueRef
    kind BLOCKS
}

Then:

GitHub adapter
    ↔ body metadata
GitLab adapter
    ↔ issue links/body depending capability
Linear adapter
    ↔ native relations where appropriate

The architectural decision should be:

Existing GitHub dependency-body syntax remains supported and canonical for GitHub, but dependency persistence is adapter-defined.

That does not reopen Phase 1. It generalizes the external representation.

⸻

4. Execution substrate abstraction

This is the centerpiece of remote workers and containers.

Right now Forge probably conceptually does:

worktree := createWorktree(...)
agent.Run(worktree)
runGates(worktree)
git.Commit(worktree)

Phase 3 needs:

ExecutionBackend
    ↓
Workspace
    ↓
commands / agent / artifacts

Something like:

type ExecutionBackend interface {
    Prepare(ctx, WorkspaceRequest) (Workspace, error)
    Execute(ctx, Workspace, Command) (CommandResult, error)
    Cleanup(ctx, Workspace) error
}

But don’t make every tiny command RPC-shaped if the agent backend itself needs a long-running environment.

Better domain:

ExecutionEnvironment
├── workspace identity
├── filesystem root
├── command executor
├── agent session capability
├── resource limits
├── environment metadata
└── artifact transport

Backends:

LocalHostBackend
ContainerBackend
RemoteWorkerBackend

The scheduler should not know which one it got.

⸻

5. Containerized execution

Container execution should come before remote workers.

It forces you to remove local-host assumptions while keeping networking and control relatively simple.

Example config:

execution:
  backend: container
  container:
    image: ghcr.io/example/forge-worker:latest
    cpu: 4
    memory: 8Gi

The workspace lifecycle:

Forge
  │
  ├─ create isolated git workspace
  │
  ├─ launch container
  │     workspace mounted/attached
  │
  ├─ run coding agent
  │
  ├─ run deterministic gates
  │
  ├─ retrieve diagnostics/artifacts
  │
  └─ destroy container

Important security boundary

The controller should own credentials wherever practical.

Do not casually inject a GitHub token with repository-wide write access into every coding-agent container.

Prefer:

agent container
    → filesystem/code operations
Forge controller
    → tracker mutations
    → push
    → PR creation
    → CI API

If the agent needs dependency downloads, give it network access appropriate to the repo, but avoid SCM mutation credentials.

Your existing agent boundary already points in exactly this direction.

⸻

Container images

Support two modes eventually:

repository-defined
Forge-generated/default

MVP:

execution:
  container:
    image: ...

Later Forge might build from:

.devcontainer
Dockerfile
Nix
toolchain discovery

Do not start there unless you want Phase 3 to become Phase 3 through Phase 11.

⸻

6. Remote workers

Once ExecutionEnvironment works locally and in containers, remote workers are mostly placement + transport + lifecycle.

Architecture:

                    Forge Controller
                          │
                    Worker Registry
                          │
             ┌────────────┼────────────┐
             ▼            ▼            ▼
          worker-1     worker-2     worker-3
             │            │            │
          workspace     workspace    workspace
             │
           agent
           gates

The controller remains authoritative for:

execution state
dependency graph
tracker state
PR/MR state
CI supervision
retry budgets
leases

Workers are disposable executors.

That is important.

Do not turn each worker into a little Forge controller. Distributed consensus is not a hobby anybody should voluntarily acquire.

⸻

Remote worker protocol

Workers should advertise capabilities:

WorkerCapabilities
  os
  arch
  cpu
  memory
  docker
  gpu[]
  labels[]
  agent_backends[]

A job declares requirements:

ExecutionRequirements
  os?
  arch?
  cpu
  memory
  container_required?
  gpu?
  labels[]

Forge placement is deterministic:

eligible workers
  → available capacity
  → stable scheduling policy

No LLM anywhere near this.

Naturally.

⸻

Worker leases

Remote execution means failure semantics matter.

Every assigned job should have:

lease_id
worker_id
execution_id
issue_id
heartbeat
lease_expiry

If a worker disappears:

lease expires
    ↓
execution marked LOST
    ↓
workspace treated as non-authoritative
    ↓
job may be retried elsewhere

The controller should assume workers can vanish at any moment.

Because eventually one absolutely will, five seconds after claiming it “never does that.”

⸻

Repository transfer

I would not stream worktrees from the controller.

Remote workers should normally:

clone/fetch repository
checkout exact commit/ref

using constrained read credentials.

Forge then supplies:

repository
source commit
branch name
execution metadata

At completion:

Option A, cleaner:

worker returns patch/commit bundle
controller publishes

Option B:

worker pushes with scoped credentials

I strongly prefer controller-owned publication for MVP because it preserves your existing authority boundary.

So:

worker:
  clone
  modify
  test
  commit locally
controller:
  receive resulting commit/bundle
  verify
  push
  create PR

That also makes rogue or compromised workers less exciting.

⸻

7. Local and remote should use the same worker protocol eventually

A very clean endpoint would be:

Forge Controller
      │
      ▼
ExecutionBroker
      │
 ┌────┼─────────┐
 │    │         │
 ▼    ▼         ▼
local container remote

Where local is effectively an in-process worker.

This prevents maintaining two execution engines forever.

But I would evolve into that rather than immediately RPC-ing localhost because architectural purity demanded another protobuf.

⸻

8. Phase 3 frozen principles

I’d freeze these before ticketing:

1. Tracker, SCM, and CI are separate provider capabilities.
2. GitHub is one composition of those capabilities, not the Forge domain model.
3. Issue identifiers are opaque provider-qualified references, not integers.
4. Dependency edges are provider-neutral internally; persistence encoding belongs to adapters.
5. Execution location is abstracted behind an execution environment/backend.
6. The Forge controller remains authoritative for orchestration and external mutations.
7. Workers are disposable and lease-based.
8. Container execution is the first non-host execution backend.
9. Remote workers do not become independent Forge schedulers.
10. Stacked branch ancestry is distinct from logical dependency state.
11. Stacked execution may begin on dependency code before merge, but final merge ordering remains dependency-ordered.
12. Stacked-branch MVP supports single-parent chains only.
13. Routine branch restacking is deterministic; semantic merge conflicts return to an agent.
14. Workers should not receive broad tracker/SCM mutation credentials.

⸻

9. Suggested Phase 3 state additions

Keep the existing issue state machine mostly intact.

Add execution-environment concepts rather than contaminating IssueState.

WorkspaceState
  REQUESTED
  PREPARING
  READY
  LOST
  CLEANING
  CLOSED
WorkerState
  AVAILABLE
  BUSY
  DRAINING
  OFFLINE
ExecutionPlacement
  backend
  worker_id?
  workspace_id
  lease_id?

For stacked branches:

ImplementationBase
  type: BASE_BRANCH | ISSUE_BRANCH
  issue_ref?
  ref
  commit_sha

This is much cleaner than introducing states like:

WAITING_FOR_REMOTE_STACKED_GITLAB_CONTAINER

although I’m sure enterprise software would eventually get there if left unsupervised.

⸻

10. Phase 3 MVP boundary

I would not try to land all five features as one undifferentiated blob.

The vertical MVP should prove portability in this sequence:

provider abstraction
      ↓
GitLab SCM/tracker/CI
      ↓
container backend
      ↓
remote worker using container backend

Linear and stacked branches can develop alongside portions of this, but they prove different things.

The end-of-phase acceptance test should be:

Linear issue
     │
     ▼
GitHub repository
     │
Forge scheduler
     │
remote worker
     │
containerized workspace
     │
implementation agent
     │
gates / review
     │
controller pushes
     │
GitHub PR / CI

and separately:

GitLab issue
   ↓
GitLab repository
   ↓
Forge
   ↓
container/remote execution
   ↓
GitLab MR
   ↓
GitLab pipeline

plus:

Issue A
   ↓
Issue B
   ↓
Issue C
A, B, C implemented concurrently
using Forge-managed stacked branches
then merged A → B → C

If all three work, Forge has genuinely crossed the boundary from GitHub automation tool running locally to portable software-engineering orchestration system.

⸻

11. Suggested implementation epics

I’d structure Phase 3 into roughly these architectural tickets:

* P3-01: Provider-neutral tracker/SCM/CI domain model
* P3-02: Refactor GitHub implementation behind provider interfaces
* P3-03: GitLab tracker adapter
* P3-04: GitLab SCM/change-request adapter
* P3-05: GitLab CI/merge-requirement adapter
* P3-06: Opaque/provider-qualified issue references
* P3-07: Linear tracker adapter
* P3-08: ExecutionEnvironment abstraction
* P3-09: Container execution backend
* P3-10: Worker protocol and capability model
* P3-11: Remote worker leases and placement
* P3-12: Remote workspace/result transport
* P3-13: Controller-owned remote publication
* P3-14: Explicit implementation-base model
* P3-15: Single-parent stacked branch execution
* P3-16: Restacking after dependency merge
* P3-17: Stacked change-request supervision and ordered merge
* P3-18: Cross-provider/end-to-end Phase 3 test matrix

The dependency shape is roughly:

P3-01
 ├── P3-02
 │    ├── P3-03/04/05
 │    └── P3-06 ──> P3-07
 │
 ├── P3-08
 │    └── P3-09
 │         └── P3-10
 │              ├── P3-11
 │              ├── P3-12
 │              └── P3-13
 │
 └── P3-14
      └── P3-15
           └── P3-16
                └── P3-17
everything
    ↓
P3-18

The architectural theme for the phase is therefore much tighter than the feature list initially suggests:

Phase 3 removes locality and provider assumptions from Forge.

Phase 1 made implementation orchestration deterministic. Phase 2 made planning orchestration deterministic. Phase 3 makes both of those portable across providers and execution substrates, while stacked branches remove the remaining artificial serialization imposed by merge-before-dependent-execution.
