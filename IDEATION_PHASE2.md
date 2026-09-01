This is the architecture I’d freeze for Phase 2. The central change is to stop thinking of /wayfinder → /to-spec → /to-tickets as three prompts and treat them as a planning compiler with LLM-powered semantic passes. Much less ritual sacrifice to the token gods.

Forge Phase 2 Architecture: Planning Automation

Executive decision

Phase 2 should introduce a deterministic planning runtime whose artifacts form a provenance graph:

Project / Goal
      │
      ▼
Decision Set
      │
      ▼
Specification
      │
      ▼
Ticket Plan
      │
      ▼
Implementation Issues
      │
      ▼
Phase 1

Each downstream artifact records the revision hashes of the inputs from which it was derived.

That gives Forge build-system semantics:

upstream input changes
        ↓
derived artifact becomes STALE
        ↓
Forge refuses to consume it
        ↓
regenerate / review / approve

This is the key to both normal planning and replanning.

The model owns semantic judgment. Forge owns navigation, persistence, provenance, state transitions, validation, tracker mutation, and scheduling.

⸻

1. Resolved architectural decisions

1.1 Forge gets a planning domain model, but not a shadow tracker

Forge should have domain types for:

Project
Decision
Specification
TicketPlan
PlanningContext

but not every type is its own database object or tracker entity.

Canonical persistence:

Concept	Canonical representation
Project	tracker issue
Goal	structured section of Project
Constraints / non-goals	structured sections of Project
Unknown	transient agent output
Decision	tracker issue
Specification	tracker issue
TicketPlan	versioned Forge snapshot attached to Specification
Implementation Ticket	tracker issue
Planning execution state	Forge runtime DB
Compiled PlanningContext	derived/cacheable

An Unknown is not a first-class persistent entity.

An unknown becomes a Decision only when the model judges that it is consequential enough to affect the specification or implementation plan.

That avoids a tracker full of issues such as:

“Should this helper maybe be a struct?”

which is how civilizations collapse.

⸻

1.2 Tracker artifacts remain authoritative

SQLite may contain:

planning execution id
current runtime status
retry counters
needs-info checkpoints
approval checkpoints
artifact hashes
cached PlanningContext
lease ownership
tracker ETags / updated timestamps

It must not be the only place containing:

goal
decision outcome
spec
ticket plan

Deleting Forge’s local state must not destroy the project plan.

Forge should be able to reconstruct planning state from the repository and tracker.

⸻

1.3 Planning is revisioned by content, not merely mutable status

Define hashes over normalized Forge-owned content.

For example:

project_revision
  = hash(goal + constraints + scope + non-goals)
decision_revision
  = hash(question + resolution + consequences)
spec_revision
  = hash(canonical specification)
ticket_plan_revision
  = hash(spec_revision + canonical ticket plan)

Approvals bind to a specific hash.

Therefore:

approve spec ABC
edit spec
spec becomes DEF

means approval of ABC no longer applies.

No elaborate version-control pantomime is necessary.

⸻

1.4 User-facing planning should be one orchestration command

Primary UX:

forge plan <project>

Not:

forge wayfind
forge to-spec
forge to-tickets

Those are implementation stages, not things humans should have to manually conduct like some particularly tedious orchestra.

For debugging/testing, support:

forge plan <project> --until wayfinding
forge plan <project> --until spec
forge plan <project> --until tickets

Other useful commands:

forge status <project>
forge approve <project> spec
forge approve <project> tickets
forge resume <execution>

forge resume should work for both planning and Phase 1 executions.

⸻

2. Phase 2 state model

Do not make one enormous enum containing every possible combination of stage, approval, staleness, and human intervention.

Separate three dimensions.

Planning stage

WAYFINDING
SPECIFICATION
TICKET_PLANNING
READY_FOR_EXECUTION

Runtime status

ACTIVE
NEEDS_HUMAN
NEEDS_APPROVAL
FAILED
COMPLETE

Artifact freshness

FRESH
STALE

Artifact-specific state can then remain small.

Decision

OPEN
READY
NEEDS_HUMAN
RESOLVED
SUPERSEDED
STALE

Specification

DRAFT
REVIEWING
APPROVED
STALE

TicketPlan

DRAFT
REVIEWING
APPROVED
MATERIALIZED
STALE

The project issue may display the current phase for humans, but Forge should derive actual state from the artifact graph wherever possible.

⸻

3. Wayfinder state machine

Wayfinding should become:

           ┌───────────────┐
           │ Compile Input │
           └───────┬───────┘
                   ▼
           ┌───────────────┐
           │ Initial Survey│  LLM
           └───────┬───────┘
                   ▼
         materialize Decisions
                   │
                   ▼
        ┌─────────────────────┐
        │ Calculate Frontier  │ Forge
        └──────────┬──────────┘
                   │
       ┌───────────┴─────────────┐
       │                         │
       ▼                         ▼
ready decision             no ready decision
       │                         │
       ▼                         ▼
resolve decision       blocking unresolved?
     LLM                  /           \
       │                yes            no
       ▼                 │              │
new unknowns             ▼              ▼
       │            NEEDS_HUMAN    readiness review
       │                               LLM
       └──────────────┐             /       \
                      │          ready     missing
                      │            │         │
                      └────────────┘         ▼
                                      new Decisions

⸻

3.1 What constitutes the frontier?

The frontier is deterministic.

A decision is frontier-ready when:

state == OPEN
AND
blocking_for_spec == true
AND
all decision dependencies are RESOLVED
AND
no human response is currently required
AND
artifact is not stale

Forge calculates that.

The LLM does not need to repeatedly inspect fifty issues and announce, with all the ceremony of discovering fire, that issue 18 depends on issue 12.

⸻

3.2 What does the model decide?

The model decides:

* what consequential unknowns exist
* whether an unknown blocks specification
* what other decisions logically precede it
* the technical resolution of a decision
* whether research is sufficient
* whether human input is necessary
* what consequences follow from a decision
* whether a resolution exposes additional consequential unknowns
* whether the resulting planning state is sufficient for specification

⸻

3.3 Initial survey

The first invocation receives:

Project goal
constraints
non-goals
existing decisions
relevant RepositoryContext

and returns structured proposals:

DecisionProposal
  key
  question
  why_it_matters
  blocking_for_spec
  priority
  depends_on[]

Temporary keys allow proposals to reference each other.

Forge then:

1. validates the proposed dependency graph
2. creates Decision issues
3. replaces temporary references with real tracker IDs
4. calculates the frontier

⸻

3.4 Resolving a decision

Each decision agent returns something equivalent to:

DecisionResolution
  outcome
  rationale
  consequences[]
  assumptions[]
  new_unknowns[]
  needs_human?
  human_question?
  conflicts_with[]

Forge performs tracker mechanics afterward.

The agent does not:

create issue
add label
close issue
find parent project
update project
inspect tracker dependencies

It makes the decision.

Forge cleans up after the alleged intelligence.

⸻

3.5 New unknowns

A resolution may produce:

new_unknowns[]

Each contains:

question
why_it_matters
blocking_for_spec
dependencies

Forge materializes consequential ones as Decision issues and recalculates the frontier.

This is the important recursive behavior from /wayfinder, preserved without repeatedly re-teaching the model how GitHub works.

⸻

3.6 Wayfinding completion

Wayfinding is not complete merely because the current Decision list happens to be empty.

When no blocking frontier remains, Forge invokes a fresh Planning Readiness Reviewer.

It receives:

goal
constraints
resolved decision summaries
remaining nonblocking decisions
repository context

and returns:

READY_FOR_SPEC

or:

MORE_DECISIONS_REQUIRED
<DecisionProposal[]>

This prevents the system from prematurely declaring victory because the previous agent simply failed to notice something.

⸻

4. Wayfinder invocation model

Decision: one fresh invocation per decision

MVP should use:

one decision
=
one fresh agent invocation

Do not preserve one giant Claude session through the entire project.

Advantages

* deterministic resumability
* clean failure boundaries
* easier retry accounting
* easier backend substitution
* reduced context contamination
* straightforward auditing
* no dependency on conversation history
* eventual safe parallelism

Context reuse comes from PlanningContext, not an immortal chat session accumulating psychological damage.

⸻

Initial inventory and readiness are separate invocations

Wayfinding therefore uses three invocation roles:

PlanningSurveyAgent
DecisionAgent
PlanningReadinessReviewer

These may use the same underlying model/backend.

They are separate contracts.

⸻

5. Decision concurrency

MVP: sequential within one project

Independent projects may wayfind concurrently.

Decisions within a single project should initially execute sequentially.

This is deliberate.

Decision concurrency introduces:

semantic conflicts
decision invalidation
shared assumptions
reconciliation
partial rollback
stale contexts

for relatively modest latency reduction.

Phase 2 does not need to invent distributed architecture committees because several CPU cores looked lonely.

⸻

Future concurrency

Later, decisions could run concurrently when:

no dependency path exists between them
AND
declared affected domains are disjoint

Even then, a fresh reconciliation pass should inspect simultaneous resolutions before either becomes authoritative.

This is explicitly post-MVP.

⸻

6. Accumulated knowledge and PlanningContext

The model should never reconstruct project knowledge from 40 issue threads.

Forge compiles:

PlanningContext
├── project
│   ├── goal
│   ├── scope
│   ├── constraints
│   └── non_goals
│
├── resolved_decisions[]
│   ├── id
│   ├── question
│   ├── outcome
│   └── consequences
│
├── unresolved_decisions[]
├── assumptions[]
├── human_inputs[]
├── relevant RepositoryContext
├── current_frontier[]
└── provenance

Critical rule

PlanningContext should be compiled primarily from structured authoritative fields, not recursively generated summaries of old prose.

Decision issues therefore need compact structured resolution sections.

For example:

## Question
How should authentication state be persisted?
## Why It Matters
...
## Dependencies
Depends on:
- #123
## Resolution
Use ...
## Rationale
...
## Consequences
- ...
- ...
## Assumptions
- ...

The hundred-comment archaeological record remains available but does not enter normal model context.

⸻

7. Deterministic Forge vs LLM boundary

Operation	Owner
Fetch project	Forge
Parse project sections	Forge
Find decision issues	Forge
Calculate decision dependency graph	Forge
Calculate frontier	Forge
Pick next ready decision	Forge
Identify missing consequential decisions	LLM
Resolve technical decision	LLM
Determine whether human input is needed	LLM
Create/update/close decision issue	Forge
Label needs-info	Forge
Detect human response after checkpoint	Forge
Decide whether planning is semantically complete	LLM reviewer
Build PlanningContext	Forge
Generate spec	LLM
Validate spec schema	Forge
Judge spec completeness/coherence	LLM reviewer
Generate implementation decomposition	LLM
Validate ticket DAG	Forge
Judge decomposition quality	LLM reviewer
Create implementation issues	Forge
Populate dependencies	Forge
Hand issues to Phase 1	Forge

A useful test remains:

If failure can be described as “the model forgot what command/API/state transition comes next,” Forge should probably own it.

⸻

8. /to-spec becomes a specification compilation stage

Readiness requirement

A project may enter specification when:

no unresolved blocking Decisions
AND
no NEEDS_HUMAN Decision
AND
PlanningReadinessReviewer == READY_FOR_SPEC

Nonblocking unknowns may remain, but must appear explicitly as assumptions/deferred decisions.

⸻

Spec-generation input

The agent gets:

PlanningContext
RepositoryContext
Specification schema

It does not reread tracker history.

⸻

Spec-generation output

The LLM should return a typed structure, not arbitrary Markdown.

Conceptually:

SpecificationDraft
  title
  summary
  goals[]
  non_goals[]
  constraints[]
  requirements[]
  architecture
  data_model
  behavior
  migration
  observability
  testing
  rollout
  risks[]
  assumptions[]
  decision_refs[]

Each requirement receives a stable identifier:

REQ-001
REQ-002
REQ-003

Forge deterministically renders this into Markdown.

⸻

Canonical spec location

The Specification should be a dedicated tracker issue.

Reasons:

* same system already owns project planning
* comments naturally support review
* human approval is straightforward
* no planning-only git commits are required
* implementation issues can reference it
* Forge can reconstruct state without local storage

A repository specification file can be supported later if projects want durable product documentation.

It should not be required by Forge.

⸻

Spec provenance

The spec records:

Project revision
Decision revisions
Spec revision

If a contributing decision changes:

Specification => STALE
TicketPlan => STALE

automatically.

⸻

9. Specification validation and review

First run deterministic validation.

Examples:

unique REQ IDs
all decision references exist
no unresolved blocking decision references
required sections present
no duplicate requirement IDs
spec input revisions still current

Then invoke a fresh SpecificationReviewer.

It receives:

PlanningContext
rendered specification

and returns:

APPROVED

or:

CHANGES_REQUIRED
<findings>

Findings return to a specification repair invocation.

Use a bounded planning-review repair budget analogous to Phase 1 review repair.

⸻

10. Human spec approval

After automated review succeeds, Forge optionally enters:

NEEDS_APPROVAL

MVP default should be:

planning:
  approvals:
    spec: required

Approval binds to:

spec_revision

forge approve <project> spec records an approval marker against that exact revision in the tracker.

Changing the spec invalidates approval.

This gives one meaningful human gate rather than having a human click “yes, dear robot” after every decision.

⸻

11. /to-tickets becomes ticket-plan compilation

Input:

approved Specification
PlanningContext
repository policy

Output should again be structured.

TicketPlan
  spec_revision
  tickets[]

Each proposed ticket:

Ticket
  temporary_key
  title
  objective
  requirement_ids[]
  acceptance_criteria[]
  implementation_constraints[]
  relevant_decision_ids[]
  depends_on[]

Example:

T1
  Implement planner persistence
  requirements: [REQ-004, REQ-006]
T2
  Implement wayfinder frontier
  requirements: [REQ-007]
  depends_on: [T1]

Temporary keys exist only before tracker materialization.

⸻

12. Deterministic ticket-plan validation

Forge validates:

Structural

every ticket has a title
every ticket has an objective
every ticket has >=1 acceptance criterion
all dependency targets exist
dependency graph is acyclic
temporary ticket keys are unique

Traceability

every ticket references valid requirement IDs
every implementation requirement maps to >=1 ticket
every ticket maps to >=1 requirement
all referenced Decisions exist
ticket plan references approved spec revision

Dependency correctness

no implementation ticket depends on a Decision issue
no dependency references a superseded ticket
no self-dependencies

Forge should not deterministically decide:

ticket is too large
ticket boundary is stupid
architecture decomposition is awkward
unnecessary serialization exists
acceptance criterion is semantically inadequate

Those are review judgments.

⸻

13. Ticket-plan reviewer

Invoke a fresh planning reviewer with:

approved spec
proposed TicketPlan
important resolved decisions

It evaluates:

* missing requirement coverage
* ticket sizing
* bad boundaries
* unnecessary sequencing
* excessive coupling
* architecture contradictions
* vague acceptance criteria
* tickets combining unrelated responsibilities

Output:

APPROVED

or:

CHANGES_REQUIRED
<findings referencing temporary ticket keys / REQ IDs>

Repairs receive the existing plan plus focused findings.

Do not regenerate everything from blank context unless necessary.

⸻

14. Human ticket-plan approval

Before tracker issue creation, optionally require:

planning:
  approvals:
    tickets: required

MVP should default to required.

The proposed TicketPlan is persisted as a Forge-owned versioned snapshot attached to the Specification issue.

It contains:

spec_revision
ticket_plan_revision
canonical structured TicketPlan
human-readable rendering

Approval binds to ticket_plan_revision.

This avoids creating and deleting fourteen GitHub issues every time someone dislikes the decomposition.

⸻

15. Ticket materialization

After approval:

Phase A

Create all implementation issues with:

forge:materializing

and no executable status.

Collect actual tracker IDs.

Phase B

Replace:

T1
T2
T3

dependencies with:

#501
#502
#503

Update issue bodies with canonical dependency metadata.

Phase C

Re-fetch and validate the resulting tracker DAG.

Only when the entire graph is valid does Forge remove the materializing state and make the issues eligible for Phase 1.

This prevents another Forge execution from grabbing half-created tickets because computers are nothing if not opportunistic little bastards.

⸻

16. Exact Phase 1 handoff contract

Forge-generated implementation issues should contain both human-readable sections and compact machine-owned provenance.

Conceptually:

<!-- forge
kind: implementation
project: 300
spec: 320
spec_revision: sha256:abc...
ticket_plan_revision: sha256:def...
requirements:
  - REQ-004
  - REQ-007
decisions:
  - 305
  - 309
-->
## Objective
Implement ...
## Requirements
- REQ-004
- REQ-007
## Acceptance Criteria
- [ ] ...
- [ ] ...
## Implementation Constraints
- ...
## Dependencies
Depends on:
- #501
- #504

This preserves the already-frozen dependency format.

Before an issue enters READY, Phase 1 may deterministically compile:

ImplementationPlanningContext
├── objective
├── acceptance criteria
├── corresponding spec requirements
├── relevant decision outcomes
├── implementation constraints
└── spec revision

The coding agent therefore does not navigate the planning issue tree.

Forge does.

⸻

17. Human-in-the-loop model

There are only three meaningful human interaction categories.

Needs information

Decision agent returns:

NEEDS_HUMAN
question
reason

Forge:

1. labels the Decision issue
2. posts the question
3. records a comment checkpoint
4. stops the affected planning path

MVP resumes exactly like Phase 1:

forge resume <execution>

Forge looks for human responses after the checkpoint.

⸻

Spec approval

One gate per spec revision.

automated review
    ↓
human approval

⸻

Ticket-plan approval

One gate per ticket-plan revision.

automated review
    ↓
human approval
    ↓
issue creation

Both approvals should be configurable later, but required-by-default is appropriate while Phase 2 proves itself.

⸻

18. Replanning semantics

Replanning should use invalidation, not rollback.

This is another place where artifact provenance earns its keep.

Suppose:

Decision D4 changes

and the current spec says:

derived_from D1@a, D2@b, D4@c

but D4 is now revision d.

Then:

Spec => STALE
TicketPlan => STALE
unstarted implementation tickets => stale plan

No LLM is required to notice this.

⸻

Implementation discovers an invalid assumption

Phase 1 should gain one additional structured terminal outcome:

REPLAN_REQUIRED
  reason
  evidence
  affected_requirements[]
  suggested_question

Forge then:

1. blocks the affected issue
2. stops scheduling its downstream dependents
3. creates or reopens a Decision
4. marks dependent planning artifacts stale
5. returns the Project to planning
6. reruns forge plan

The implementation worker does not start redesigning the project because it got bored.

⸻

Completed work

Merged/completed work is historical fact.

Forge never automatically rolls it back.

During replan, completed work enters PlanningContext as:

implemented_facts[]

The planner may then:

keep it
build around it
create compensating work
declare later planned work obsolete

⸻

Existing unstarted tickets

When a replacement ticket plan is approved:

* old unstarted tickets no longer represented by the new plan are marked superseded
* they are closed rather than rewritten into unrelated work
* newly required work gets new issues

This preserves auditability.

⸻

In-flight work

MVP should be conservative.

If a project enters REPLAN_REQUIRED:

do not schedule additional project tickets

Existing work may be allowed to finish its current local operation, but Forge must not treat its result as valid against the new plan until the replan is complete.

Automatic semantic compatibility detection is post-MVP.

⸻

19. Remaining genuine unknowns

There are no architecture-blocking unknowns needed before implementation.

Several deliberate post-MVP research areas remain:

A. Large planning histories

At some point hundreds of resolved Decisions may exceed reasonable context.

Possible later solution:

structured relevance indexing
+
requirement / domain scoped retrieval

Do not build it yet.

B. Concurrent decision resolution

Possible, but requires semantic conflict reconciliation.

Sequential is correct for MVP.

C. Cross-project shared Decisions

A decision such as:

“All services use protobuf schema X”

could eventually become organization/repository architecture knowledge rather than project-local knowledge.

That is a separate knowledge-management problem and should not infect Phase 2 MVP.

⸻

20. Phase 2 MVP boundary

MVP proves exactly:

existing project issue
        ↓
LLM identifies consequential decisions
        ↓
Forge deterministically manages decision loop
        ↓
LLM produces reviewed spec
        ↓
human approves spec
        ↓
LLM produces reviewed TicketPlan
        ↓
human approves TicketPlan
        ↓
Forge creates valid implementation DAG
        ↓
Phase 1 executes it

MVP includes:

* GitHub/tracker-backed Project
* Decision issues
* sequential decision execution
* PlanningContext compilation
* planning readiness reviewer
* Specification issue
* deterministic spec validation
* fresh spec review
* spec approval
* typed TicketPlan
* deterministic DAG/coverage validation
* fresh ticket-plan review
* ticket-plan approval
* tracker materialization
* exact Phase 1 handoff metadata
* manual needs-info resume
* provenance/staleness
* conservative REPLAN_REQUIRED

MVP explicitly excludes:

* concurrent Wayfinder decisions
* automatic polling/webhooks
* cross-project knowledge
* semantic impact analysis
* auto-merging plans around in-flight implementation
* long-history vector retrieval
* multiple collaborating architect agents
* elaborate planning dashboards

One competent model plus deterministic orchestration first.

The clown car can come later if measurements establish an actual need.

⸻

21. Implementation tickets

The identifiers below are temporary planning IDs.

⸻

P2-01 — Define planning artifact schemas and tracker encoding

Scope

Introduce canonical domain structures for:

Project
Decision
Specification
TicketPlan
RequirementReference
Planning provenance

Define tracker representation and Forge-owned metadata.

Acceptance criteria

* Project goal, constraints, scope, and non-goals can be parsed deterministically.
* Decision issues support question, rationale, dependencies, state, resolution, consequences, and assumptions.
* Specification issues contain stable requirement IDs.
* Forge metadata records project/spec/plan provenance without replacing human-readable bodies.
* Planning artifact revisions are calculated from normalized relevant content.
* Existing Phase 1 dependency body syntax remains unchanged.
* Unit tests cover parse/render round trips.

Dependencies

None.

⸻

P2-02 — Implement planning provenance and state engine

Scope

Implement stage/status/freshness handling and derived-artifact invalidation.

Acceptance criteria

* Forge models planning stage separately from runtime status.
* Artifact freshness is calculated from recorded input revisions.
* Changing a Decision invalidates dependent Specification and TicketPlan artifacts.
* Changing a Specification invalidates its TicketPlan.
* Approval records bind to artifact revision hashes.
* Stale artifacts cannot progress to downstream stages.
* Only one active planning execution may own a Project at a time.
* Planning state can be reconstructed from tracker state plus Forge runtime metadata.

Dependencies

* P2-01

⸻

P2-03 — Implement PlanningContext compiler

Scope

Compile normalized model context from tracker artifacts and existing repository context.

Acceptance criteria

* PlanningContext includes goal, constraints, non-goals, resolved decisions, unresolved decisions, assumptions, human inputs, frontier, and provenance.
* Decision comments/history are not included by default.
* Resolved Decisions use compact structured outcomes.
* Current tracker state is revalidated before context compilation.
* Existing RepositoryContext is reused rather than independently rediscovered.
* Compiled context is cacheable and invalidated when source revisions change.
* Tests demonstrate that repeated invocations do not reread irrelevant historical issue content.

Dependencies

* P2-01

⸻

P2-04 — Add structured planning-agent contracts

Scope

Extend the generic Agent boundary with Phase 2 structured invocation contracts.

Required contracts

PlanningSurvey
DecisionResolution
PlanningReadinessReview
SpecificationGeneration
SpecificationReview
TicketPlanGeneration
TicketPlanReview

Acceptance criteria

* All planning invocations accept typed normalized input.
* All outputs are schema validated.
* Invalid structured responses fail predictably.
* Reviewer invocations are fresh agent contexts.
* Repair invocations receive focused reviewer findings.
* Planning-specific behavior does not require scheduler knowledge.
* Claude Code works through the existing backend abstraction.

Dependencies

* P2-01

⸻

P2-05 — Implement deterministic Wayfinder engine

Scope

Implement survey → decision frontier → resolution → readiness loop.

Acceptance criteria

* Initial survey can propose Decisions with temporary dependency references.
* Forge materializes Decision issues and resolves their tracker IDs.
* Decision dependencies form a validated DAG.
* Frontier calculation is deterministic.
* Ready Decisions execute one at a time.
* Decision resolutions can produce new unknowns.
* New consequential unknowns become Decisions.
* NEEDS_HUMAN decisions stop the affected planning path.
* Empty blocking frontier triggers a fresh readiness review.
* Wayfinding completes only on READY_FOR_SPEC.
* Agent code performs no tracker mechanics.

Dependencies

* P2-02
* P2-03
* P2-04

⸻

P2-06 — Implement planning human checkpoints and approvals

Scope

Provide reusable Phase 2 human-intervention mechanics.

Acceptance criteria

* Decision NEEDS_HUMAN posts a tracker question and records a checkpoint.
* forge resume detects responses newer than that checkpoint.
* Spec approvals bind to a spec revision.
* Ticket-plan approvals bind to a ticket-plan revision.
* Mutating an approved artifact invalidates its approval.
* Approval state is visible in the tracker.
* Approval requirements are configurable through .forge.yaml.
* No project planning content is stored solely in local Forge state.

Dependencies

* P2-01
* P2-02

⸻

P2-07 — Implement specification generation and review pipeline

Scope

Convert completed wayfinding state into a reviewed Specification.

Acceptance criteria

* Forge refuses spec generation while blocking Decisions remain unresolved.
* Specification agent receives compiled PlanningContext.
* Model output uses structured requirements with stable IDs.
* Forge deterministically renders the canonical spec.
* Deterministic validation detects malformed/duplicate references and stale inputs.
* A fresh SpecificationReviewer runs after deterministic validation.
* CHANGES_REQUIRED findings enter a bounded repair loop.
* Approved automated review transitions to configured human approval.
* Canonical specification is stored in a dedicated tracker issue.
* Spec records project and Decision revision provenance.

Dependencies

* P2-02
* P2-03
* P2-04

⸻

P2-08 — Implement TicketPlan generation, validation, and review

Scope

Transform an approved Specification into a validated implementation DAG proposal.

Acceptance criteria

* Generator receives approved spec and normalized planning context.
* TicketPlan uses temporary ticket keys before materialization.
* Every ticket contains objective, requirement references, acceptance criteria, and dependencies.
* Forge validates dependency references and rejects cycles.
* Forge validates spec requirement coverage.
* Forge rejects unknown requirement/Decision references.
* Forge rejects tickets without acceptance criteria.
* Fresh TicketPlanReviewer evaluates decomposition quality.
* Review findings support bounded repair.
* Reviewed plans can enter human approval.
* TicketPlan snapshot persists outside ephemeral Forge memory and carries a revision hash.

Dependencies

* P2-02
* P2-03
* P2-04
* P2-07

⸻

P2-09 — Materialize approved TicketPlans and implement Phase 1 contract

Scope

Create implementation issues and make the resulting graph directly executable by Phase 1.

Acceptance criteria

* All issues are initially created in a non-executable materializing state.
* Temporary ticket references are converted to tracker issue IDs.
* Generated bodies use the existing canonical ## Dependencies syntax.
* Issues contain project, spec revision, plan revision, requirement IDs, and relevant Decision references.
* Forge re-fetches and validates the materialized DAG.
* Issues become executable only after the complete graph validates.
* Phase 1 rejects stale Forge-generated planning metadata.
* Phase 1 normalized context includes relevant spec requirements and Decision outcomes without agent tracker navigation.
* External dependencies retain existing Phase 1 semantics.

Dependencies

* P2-01
* P2-08

⸻

P2-10 — Implement unified planning CLI lifecycle

Scope

Expose Phase 2 through a small deterministic command surface.

Acceptance criteria

Supported workflow includes:

forge plan <project>
forge plan <project> --until wayfinding
forge plan <project> --until spec
forge plan <project> --until tickets
forge status <project>
forge approve <project> spec
forge approve <project> tickets
forge resume <execution>

Additional criteria:

* forge plan is idempotent.
* Re-running it resumes from current valid artifact state.
* It stops cleanly at human gates.
* It never regenerates fresh approved artifacts unnecessarily.
* Status shows stage, active Decision, approvals, stale artifacts, and next deterministic action.

Dependencies

* P2-05
* P2-06
* P2-07
* P2-08
* P2-09

⸻

P2-11 — Add conservative replanning and Phase 1 escalation

Scope

Implement the minimum safe replan loop.

Acceptance criteria

* Phase 1 agents may return structured REPLAN_REQUIRED.
* Forge records the evidence and affected requirement references.
* Relevant downstream implementation issues stop becoming READY.
* Forge creates or reopens a planning Decision.
* Upstream changes deterministically mark derived artifacts stale.
* Completed/merged tickets are retained as historical facts.
* Superseded unstarted tickets are closed rather than silently repurposed.
* Replanning produces a new spec/plan revision.
* New plan approval is required before stale work resumes.
* Forge does not attempt automatic semantic compatibility of in-flight work.

Dependencies

* P2-02
* P2-05
* P2-07
* P2-08
* P2-09

⸻

P2-12 — Add end-to-end Phase 2 workflow tests

Scope

Exercise the complete planning compiler into Phase 1.

Acceptance criteria

Test scenarios cover:

1. goal requiring no Decisions
2. multiple dependent Decisions
3. Decision spawning another blocking Decision
4. NEEDS_HUMAN and manual resume
5. spec review rejection and repair
6. stale spec caused by changed Decision
7. ticket-plan cycle rejection
8. missing requirement coverage rejection
9. ticket-plan review repair
10. human approval revision mismatch
11. successful issue materialization
12. generated DAG accepted by Phase 1
13. REPLAN_REQUIRED invalidating downstream artifacts
14. recovery after Forge local cache/state loss where canonical tracker state remains intact

Dependencies

* P2-05
* P2-06
* P2-07
* P2-08
* P2-09
* P2-10
* P2-11

⸻

22. Ticket dependency DAG

P2-01  Planning schemas
  │
  ├──────────────┬────────────────┐
  ▼              ▼                ▼
P2-02          P2-03            P2-04
State          Context           Agent contracts
  │              │                │
  ├──────────────┴───────┬────────┘
  │                      │
  ▼                      ▼
P2-06                  P2-05
Human gates            Wayfinder
                         │
       ┌─────────────────┼──────────────┐
       │                 │              │
       ▼                 │              │
     P2-07 ◄─────────────┘              │
     Spec                               │
       │                                │
       ▼                                │
     P2-08                              │
     TicketPlan                         │
       │                                │
       ▼                                │
     P2-09                              │
     Materialize / Phase 1              │
       │                                │
       ├───────────────┐                │
       ▼               ▼                │
     P2-10           P2-11 ◄────────────┘
     CLI             Replanning
       │               │
       └───────┬───────┘
               ▼
             P2-12
             E2E

⸻

23. Suggested execution waves

Wave 1

P2-01

Freeze the planning contracts before everyone invents a slightly different concept of a Decision.

Wave 2

Parallel:

P2-02
P2-03
P2-04

State/provenance, context compilation, and agent contracts are largely independent once schemas exist.

Wave 3

Parallel:

P2-05
P2-06
P2-07

Wayfinding, human checkpoints, and spec pipeline can proceed against the frozen interfaces.

Wave 4

P2-08

Ticket planning depends on the spec contract being stable.

Wave 5

P2-09

Materialize plans and establish the Phase 1 boundary.

At this point the core Phase 2 vertical slice works.

Wave 6

Parallel:

P2-10
P2-11

Complete UX and conservative replanning.

Wave 7

P2-12

Full workflow/golden integration testing.

⸻

Final architecture in one picture

                              TRACKER
                                │
                   ┌────────────┴────────────┐
                   │                         │
             Project Issue             Decision Issues
                   │                         │
                   └────────────┬────────────┘
                                │
                         deterministic
                      PlanningContext compiler
                                │
                                ▼
                     ┌─────────────────────┐
                     │     WAYFINDING      │
                     │                     │
                     │ Forge: frontier     │
                     │ LLM: decisions      │
                     └──────────┬──────────┘
                                │
                         readiness review
                                │
                                ▼
                     ┌─────────────────────┐
                     │   SPECIFICATION     │
                     │                     │
                     │ LLM: synthesis      │
                     │ Forge: validation   │
                     │ LLM: review         │
                     │ Human: approval     │
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │    TICKET PLAN      │
                     │                     │
                     │ LLM: decomposition  │
                     │ Forge: DAG checks   │
                     │ LLM: review         │
                     │ Human: approval     │
                     └──────────┬──────────┘
                                │
                      deterministic materialize
                                │
                                ▼
                       Implementation DAG
                                │
                    spec/requirement provenance
                                │
                                ▼
                     ┌─────────────────────┐
                     │      PHASE 1        │
                     │                     │
                     │ schedule            │
                     │ worktree            │
                     │ implement           │
                     │ gates               │
                     │ review              │
                     │ PR / CI             │
                     └─────────────────────┘

The useful conceptual simplification is:

Phase 1 is an execution engine. Phase 2 is a planning compiler.

Wayfinder becomes its iterative semantic-analysis pass. The spec is an intermediate representation. Ticket planning lowers that representation into an executable dependency graph. Forge handles symbol-table-grade bookkeeping, provenance, graph validation, and state transitions; models are invoked only where someone actually has to understand what the software should do.

That keeps the good part of Pocock’s workflow while removing the repeated “let me inspect the issue tracker and figure out what the ritual says I do next” tax that motivated Forge in the first place.
