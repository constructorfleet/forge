# Live-agent TUI: observe and control one Execution

A Forge TUI that shows the Workers of one Execution, streams each Worker's
Agent transcript live, and offers a full control surface — cancel / retry /
approve / answer-a-decision — plus answering `NEEDS_INFO` inline. Reachable
two ways: `forge execute` renders it when interactive, and `forge watch [id]`
attaches to an Execution already in flight.

This is the destination of the wayfinder map (issue #442). Every load-bearing
decision below is cited to its resolving ticket; this document **states**, it
does not re-derive. Architectural rationale lives in ADRs 0030 and 0031.

**Scope:** local Executions only, one Execution's Workers, plus the
planning-phase view. See [Out of scope](#out-of-scope).

**Vocabulary:** **Worker** is the orchestrator's unit of concurrent work;
**Agent** is the coding backend. The feature's user-facing text may say
"agents"; the domain term for what the TUI lists is **Worker**.

---

## 1. Architecture: the TUI is an observer, never an owner

The TUI reads the SQLite store and issues commands; it never performs
engineering work and never owns a Worker. The Engine owns all mutation of
engineering state. See ADR 0031.

- **Observation is store-polling, one read path for both entrypoints.** A
  broker was rejected (carries only live events; attach-with-backfill still
  needs the store — "polling plus a broker", not an alternative). The
  in-process fast path for `forge execute` was rejected too (two fidelity
  levels by launch method; attach second-class). [#446]
- **One tick (~1s) fetches Worker state and transcript events together**, over
  a narrow consumer-declared read-only interface (the
  `LostRecoveryLister`/`LeaseLister` pattern, not the ~570-method `Store`).
  Cursors are per `agent_run_id`. [#446]
- **Read path skips `Migrate`** (DDL at every open) and verifies
  `schema_migrations` instead, failing loudly. Uses a **normal read-write
  handle** (a `mode=ro` handle cannot create the `-wal`/`-shm` sidecars). [#446]
- **Backpressure:** a wedged or slow TUI cannot affect an Execution. Live view
  may drop events under duress; the persisted transcript stays complete. Gaps
  are marked in the UI only, kept distinct from `TRUNCATION`. Capture is
  best-effort by contract — an Adapter must never let a sink fail or change
  `Execute`'s outcome. [#446]
- **Poller shape:** the `LostExecutionController` shape (injectable `Now` /
  `Sleep`, never abort a pass, `errors.Join`), but **owned by the TUI package**,
  not a runtime controller. `storage.PlanningLease` is an anti-model. [#446, #444]

## 2. Storage substrate (prerequisite)

The transcript substrate cannot serve sub-second tailing today. Remediation is
prerequisite work; see ADR 0030 for rationale.

| Change | Detail |
|---|---|
| WAL enabled | `journal_mode=WAL` + `synchronous(NORMAL)` in the DSN `_pragma` list, not a one-shot exec |
| Append-only writes | Batch of only unflushed events on a ~250ms timer, plus a flush at run completion; every flush uses `context.WithoutCancel(ctx)` |
| Stable `seq` | Assigned once at `Emit`, per `agent_run_id`, never renumbered; `UNIQUE (agent_run_id, seq)`; eviction leaves gaps |
| Cap in SQL | 5000 events, delete-oldest, in the same transaction as an append |
| Read API | `TranscriptEventsAfter(ctx, agentRunID, afterSeq, limit)` + a phase-agnostic live-runs enumerator returning `agent_run_id` |
| Expose run id | `AgentRunsByExecution`/`AgentRunsByIssue` select `id`; `storage.AgentRun` gains an `ID` field |
| Readers get a handle | Normal read-write handle used read-only, separate from the writer pool (writer `SetMaxOpenConns(1)` unchanged) |
| Migration | Full table rebuild (migration 0020 pattern); existing dense 0-based `seq` needs no backfill; `TRUNCATION` no longer persisted by storage |

The fidelity floor is one event per completed assistant turn. `openai` sits
below it by design (one post-hoc message) and is **labelled, not raised**;
raising `openai` drags provider streaming into a display feature. New adapters
default to post-hoc when `NewStreamParser` is nil. [#452]

**Liveness/elapsed columns (Worker-level display):** `workers.last_heartbeat`
(beat every 5s, stale at 15s) and `execution_issues.state_changed_at` (set in
the transition transaction). Liveness is per Worker, never from `owner_pid`
(EPERM→true, no pid recycle disambiguation) and never from
`agent_runs.result='RUNNING'` (durable interrupted marker). Loss is *displayed*,
never detected. A single canonical `IssueState` grouping in `internal/domain`
is consumed by both the TUI and `statusreflect` (this fixes #465 at the root);
`LOST` is not an `IssueState`. [#453]

## 3. Framework and rendered layout

**Bubble Tea v2 + Lipgloss v2 + Bubbles v2** under the `charm.land/...` vanity
path (`github.com/charmbracelet/bubbletea/v2` fails with a module-path
mismatch). Grounds for the choice (a quantified bake-off, [#445] then [#450]):

- **Frame budget is free.** v2 renders on a ticker (`defaultFPS 60` /
  `maxFPS 120`), decoupling event rate from redraw rate; tview fires a full
  `Draw` per write.
- **`Update` is a pure function**, so the TUI is testable without a terminal.
  `teatest/v2` is a convenience, not load-bearing (still pseudo-versioned under
  `x/exp`). The core v2 renderer `ultraviolet` is also pseudo-versioned — the
  old *assumed* risk is now *measured* and accepted.
- **Hand-roll a 40-line capped ring buffer** over `[]string` for the transcript
  pane (Bubbles `viewport` has no append API or line cap). Measured: 1.26 µs/
  event at a million events, frame materializes flat at 20 µs.
- **Gate on `isatty` yourself** (Bubble Tea *proceeds* on non-TTY by opening
  `/dev/tty`). This is the first terminal detection in the tree.

**Layout and IA** (from the prototype, [#450], extended by [#480], re-laid out
by [#756]):

- **Three regions.** The execution list is on top. Under it, three body panes
  sit side by side: the agents pane on the left, the live output pane in the
  centre, and the diff pane on the right. The detail strip and the footer
  close the frame. Each region has a border and a section header. The focused
  region draws a double-line border, so focus reads without colour.
- **The execution list** is a table with one row per Worker: the attention and
  liveness glyphs, the Issue id, the name, the verbatim state, the elapsed
  time, the agent count, and the newest output line. It shows at most three
  rows and scrolls to keep the selection visible; its header then names the
  visible range. The state column is colour-coded by its coarse group:
  running, passed, failed, and warning. Colour is not load-bearing.
- **The agents pane** lists the agents that worked the selected Issue: the
  implementation Agent first, then one row per review subagent, in first-seen
  order. The selected agent is highlighted. The rows derive from the store's
  per-run summary (`TranscriptAgents`), which reads no event body.
- **The output pane** shows the selected agent's transcript alone. The tailer
  keeps every event and filters the window, so a switch between agents needs
  no re-read. Gate rows show only with the implementation Agent.
- **The diff pane** opens and closes with `d`. It shows the stored unified diff
  inline, with file headers, hunk headers, additions, and deletions styled by
  kind. Its header names the changed-file count. The summary loads off the
  update goroutine, once per Review; an open pane reloads only when the
  selected row's Review changes. `enter` in the pane still hands the whole
  diff to `$PAGER` for detailed inspection.
- **The detail strip** describes the focused pane's selection: the Worker's
  verbatim `IssueState`, elapsed from `state_changed_at`, heartbeat age from
  `workers.last_heartbeat` (**never conflated**), attempt number against the
  retry budget, current tool name, and Review verdict; or the selected agent;
  or the selected transcript entry; or the diff totals.
- **Two separate glyph columns**: attention (`!` attention / `*` running a tool)
  and liveness (`•` live / `×` no beat ≤15s / blank for planning). One glyph
  with a precedence order cannot say "needs an answer **and** its orchestrator
  is gone". Prose carries no glyph.
- **Contextual footer** naming only the keys legal right now, derived from the
  same view-model as the row, so it can never advertise an illegal key. `tab`
  and `shift+tab` cycle the panes; `j`/`k` move the execution, agent, or diff
  selection of the focused pane; `enter` inspects the output pane from the list
  or the agents pane; `d` toggles the diff pane from every pane.
- **The frame is a pure function from a plain view-model struct to a string**
  (`frame.go` and `layout.go` have no framework import) — the property that
  makes the whole view testable headless. [#450]

### The transcript pane: one interleaved, annotated, linear timeline [#480]

Everything but the diff rides the transcript pane; heavy content defers out.

- **Gates** surface as **synthetic collapse rows** riding the `TOOL_RESULT`
  collapse affordance (one line, first output line, expand on demand). A gate
  is not a `TranscriptEvent`; `gate_runs` (0001+0003) is one row per gate,
  output already tail-bounded by source `textcap`; the engine's `gate.run`
  event is lean (name/command/exit_code/passed). The synthetic row expands to
  `stdout`/`stderr` + `exit_code` + `command`. No separate gate strip.
- **Diff bodies render in the pane and defer to `$PAGER`** over
  `review_runs.diff`. The pane reads at most 256 KiB and 4,000 complete lines
  off the update goroutine. It renders unified-diff lines without syntax
  lexing, and supports vertical hunk scrolling plus horizontal scrolling for
  long lines. `enter` still opens the complete stored diff in `$PAGER`.
  There is no `forge diff` subcommand (it is the live `gitDiffProducer`,
  forbidden by the store-only read path); `review_runs.diff` is the only
  store-side copy (migration 0004). Same suspend-and-return mechanic as
  #447's artifact view.
- **Multi-attempt history** is one continuous scrollback with inline
  `— attempt N —` dividers. `agent_runs` has no attempt field (order is
  insertion `id`); retries accumulate rows. "Attempt" is a derived grouping of
  adjacent runs for one Issue — a pure view-model transform, no navigation mode.
  #450's strip already labels "attempt N against the retry budget".
- **Concurrent per-axis review streams** each get a row in the agents pane,
  read straight off `transcript_events.subagent` (`bugs` / `quality` / `docs`,
  agentreviewer.go:154-156); the output pane shows one agent at a time
  ([#756]). Every event still carries its inline axis label. A Worker in
  REVIEWING holds up to 3 concurrent `agent_runs` rows; the aggregate
  `review_runs` verdict on the strip carries the outcome.
- Gate rows are synthetic — **no extension of the four `TranscriptEvent` types.**
- Glyphs: `▸` call, `└` result, `░` truncation and ring eviction (distinct
  wording: one is Forge's 5000-event window, the other the renderer's buffer),
  no glyph for prose. Expansion is per event and does not persist across
  selection change.

## 4. Entrypoints and quit semantics

- **`forge watch [id]`** attaches. One positional argument disambiguated by an
  explicit store probe of both ID spaces (`executions`, then
  `planning_executions`, then Feature via `agent_runs backend='planning'`),
  failing loudly on ambiguity — not by a cwd-relative `os.Stat` (unsound: a
  36-char UUID is a legal Feature id). A **bare `forge watch`** resolves only
  when exactly one Execution has a live Worker by the #453 heartbeat; else it
  lists candidates and exits 2. [#449, #443]
- **Interactive `forge execute`** renders the TUI; non-interactive stays today's
  silent run. Auto-detection via `isatty` with **both** overrides required:
  `--no-tui` (operator piping through `tee`) and `--tui` (Bubble Tea proceeds on
  non-TTY). CI keeps working by construction — no workflow invokes the binary.
  [#449]
- **Quitting never stops work** — not `q`, not Ctrl-C. `q` reverts to silence
  plus the end-of-run summary (`forge execute` prints nothing mid-run; stdout
  writes follow `Scheduler.Run`). **Ctrl-C is bound to `q`'s meaning and never
  reaches the run**; a second Ctrl-C falls through to Go's default handler.
  [#449]
- **Panic guard:** the first `recover()` at a command boundary, inside
  `runWatch`/`runExecute` (never `main`, since `os.Exit` skips main's defers).
  Exit codes: 0 success, 1 operational error, 2 usage. [#449]
- **Reattach backfills** everything retained (`afterSeq = 0`) and opens with an
  explicit "earlier events not retained" marker where the window evicted
  history, distinct from `TRUNCATION`. [#449]
- **Multiple watchers** are supported with no coordination (no registry, count,
  or cap); WAL makes concurrent readers cheap. A control action in one appears
  in another on the next tick. [#449]

## 5. Control seam

The TUI never performs engineering work; controls split by action, never by
launch mode. [#447]

| Control | Mechanism | Notes |
|---|---|---|
| **Cancel** | In-process: `CancelExecution` on an operational Engine (store writes + PID syscalls) | Legal with no live orchestrator; cancel-after-crash is the most valuable case. Depends on #457. Acknowledged pending-until-observed; `WaitForProcessExit` polls ≤5s |
| **Retry** | Detached `forge` child, both entrypoints | `RetryIssue` ends in `resumeIssue` — workspace setup, rebase, coding agent, repair loop, gates, commit, PR (`agent.idle_timeout` defaults to 20m for a CLI provider). One behaviour beats one that changes with launch mode |
| **Resume-after-answering** | Detached `forge` child | Same shape as retry; ships agent + repair + CI wait |
| **Approve** | In-process tracker write; reads artifact via `$PAGER` | Store-only write; pairs with `$EDITOR` as one suspend-and-return mechanic |
| **Answer `NEEDS_INFO` / Decision** | In-process tracker POST | See below |

**Child process rules:** capture stderr (every `refreshRetryBase` failure
except rebase conflict persists nothing — #458); spawn with an explicit `Dir`
at the git top-level plus absolute `--config`/`--db` (#459); reuse
`clicommon.ConfigureProcessGroup` and `DefaultRunner`'s bounded-tail
convention. Nothing in the tree detaches a child today. [#447]

**Concurrency:** nothing serializes two actors on one Issue; the TUI surfaces
failures instead of preventing them (the race is #456) and only disables a
control while its own call is in flight. `workers` row vanishes between
`ReleaseWorkerClaim` and re-claim — never render as Worker death. [#447]

**Ack model:** pending-until-observed, one-tick-consistent. No optimistic
state; destructive-action confirmation is UI-only (a frame-side confirm, since
`forge cancel` has none and should not grow one). [#447]

### Answering `NEEDS_INFO` and Decisions [#448]

- The TUI **posts the answer itself** as a plain, **marker-free** tracker
  comment and stops there. **Answering is not resuming.** The marker contract
  runs opposite to charting's assumption: execution resume recognises new human
  input by **marker absence plus tracker clock** (`resume.go:110-119`), so a
  marked answer is *skipped*. `internal/needsinfo` is imported only to **strip**
  `CommentMarker` when displaying a question.
- Composition shells out to **`$EDITOR`**. A failed post is held **in memory
  only** with the typed tracker error (no outbox — see Out of scope).
- **`verifyTrackerAuth` at startup** (one cheap `GET /repos/{owner}/{repo}`,
  free when the token is absent) disables the answer control up front; no
  offline tracker mode.
- A posted answer is invisible to a store-only reader until a resume persists
  it into `resumed_context`, so the TUI holds its own answer in session state.
- **Planning Decisions** use the same answer control but depend on #476
  (planning resume excludes comments **by author**, silently dropping a
  same-account answer).
- Three blocked states are **read-only**: `REPLAN_REQUIRED` (via `forge
  approve`, store-only), `PROVIDER_LIMIT` (`ProviderLimitController` owns the
  only exit), `review_overrides`.

## 6. Planning-phase view

In scope: one view, **two list models**, sharing the frame, transcript
renderer, poller, and controls. [#443]

- **The transcript layer unifies.** Both phases write the same event struct
  shape to `agent_runs` / `transcript_events` (migration 0020 dropped the
  `execution_issues` FKs). One renderer is right; only vocabulary differs:
  `agent_runs.backend='planning'` labels the phase, and `subagent` is the *only*
  record of the stage (empty for the execution implementation agent, never
  empty for planning).
- **Nothing above the transcript unifies.** Planning is strictly sequential
  (`wayfinding.Loop` is deliberately sequential), so **at most one planning
  `agent_runs` row is ever in flight**. The planning list is a **stage-history
  strip with one live head**: rows from `agent_runs WHERE execution_id =
  <feature-id> AND backend='planning'`, labelled by the transcript's `subagent`
  — the six reachable stage keys
  `decision-resolution` / `planning-readiness-review` / `specification-generation` /
  `specification-review` / `ticket-plan-generation` / `ticket-plan-review`
  (`planning-survey` has no caller). Coarse position from run history, not from
  `DeriveStage` (which reads the filesystem — the read path is store-only).
- **Planning gets no liveness claim.** `finished_at` is never NULL (placeholder
  = `started_at`), and nowhere to attach a heartbeat. Show "last activity at T"
  only. [#443]
- **Controls:** no `forge cancel` for planning. Available: approve and
  answer-a-decision. Answering a paused Decision needs **network and auth**
  (the human input channel is the tracker, not the database). [#443]

## 7. Sensitive / non-obvious invariants (do not "simplify" away)

- **Capture is best-effort by contract.** A `TranscriptSink` must never fail or
  change `Execute`'s outcome; a TUI reader inherits that — it is never
  load-bearing.
- **Payloads are already bounded** by the emitter via `internal/textcap`
  (4000-byte `MaxDiagnosticLen`, byte cap keeping the tail); the renderer does
  not defend against unbounded strings. `textcap` is a **named limitation** —
  not rune-aware, no original-length record, field truncation emits no
  `TRUNCATION` event — repaired only as adapter-side fidelity, never here.
- **`seq` must never be treated as a user-facing sequence** — it is a stable
  arrival ordinal; eviction leaves gaps.
- **Elapsed and heartbeats are two sources that must never be conflated** in the
  view model (#450's prototype bug: a Worker silent 6m into a 41m state must
  not read "no beat for 41m").
- **`owner_pid` and `agent_runs.result` are not liveness.**
- **The TUI never writes engineering state** — no intent queue, no durable
  outbox, no `Migrate`.
- **Demo/lint isolation:** nothing in CI invokes the binary, so TUI
  dependencies and terminal detection cannot break CI by construction.

## 8. Out of scope

- **Local LOST recovery** — detection mutates state (`RecordGateFailure` +
  `ResumeExecution`) and needs execution-correctness decisions; it is not a
  display feature. #453 kept loss *display* and ruled loss *detection* out.
- **Pause** — a new Engine capability (no PAUSED Issue state; enforcement would
  be the scheduler's dispatch decision carrying the stall-detector problem).
- **Remote and container backend streaming** — observing off-host Workers needs
  a transcript transport (ADRs 0020/0025/0029).
- **A machine-wide, multi-Execution dashboard** — the destination is one
  Execution's Workers (ADR 0010's one-execution-per-run shape).
- **A durable answer outbox** — would mean the TUI writing engineering state,
  contradicting the observer invariant, and needs idempotency the GitHub
  comment API does not offer. A failed post is held in memory, retried by hand.
- **Raising adapter fidelity (esp. `openai`)** — dragging provider streaming
  into a display feature; labelled, not raised.

## 9. Acceptance scenarios (when is this done?)

An operator can, against a **local** Execution:

1. `forge watch <id>` attach mid-flight and **tail a live transcript** with
   tool calls collapsed; scroll back through attempts (inline dividers); see
   per-axis review labels during REVIEWING; see gate failures as expandable
   rows.
2. `forge watch` with one live Execution attach to it by default; with several,
   list candidates and exit 2.
3. `forge execute` in a TTY render the TUI live; piped through `tee` render
   today's silent run (`--no-tui`); force `--tui` where needed.
4. **Cancel** a Worker and see PENDING-until-observed flip to CANCELLED;
   **retry** a FAILED Worker via a detached child (work continues even after
   the watcher quits); **approve** a `REPLAN_REQUIRED` Issue.
5. **Answer a `NEEDS_INFO`** inline in `$EDITOR`; the posted comment is plain
   and marker-free; the control is disabled up front on a bad credential.
6. Pause-free: `q` and Ctrl-C leave work running; only a second Ctrl-C kills the
   process, having already detached any control child.
7. A Worker silent inside a long tool call still shows a live heartbeat; a
   wedged one shows `×` at 15s; planning rows show "last activity at T" with no
   liveness claim.
8. A wedged TUI cannot stall the Execution: capture stays best-effort, the
   persisted transcript is unaffected, and a reattach backfills from `afterSeq
   = 0`.

---
*Resolved via wayfinder map #442. ADRs 0030, 0031, CONTEXT.md, and the
`ready-for-agent` implementation issues are the executable ground truth; this
spec is the reference they cite.*
