# The TUI is a passive observer and action-issuer, never a Worker owner

The live-agent TUI observes one Execution and issues control actions, but it
never performs engineering work and never owns a Worker. The Engine owns all
mutation of engineering state; the TUI reads the SQLite store and issues
commands that the Engine (or a detached `forge` child) carries out. This
boundary, fixed across this effort, is why the TUI can never stall or corrupt
an Execution, why it is safe to attach and detach at any time, and why "attach"
demands nothing of the orchestrator.

- **Observation is store-polling, one read path for both entrypoints.** A
  broker was rejected: it carries only live events, so attach-with-backfill
  still needs the store, making it "polling plus a broker", not an
  alternative. The read path skips `Migrate` (DDL on every open) and verifies
  `schema_migrations` instead, failing loudly. One tick (~1s) fetches Worker
  state and transcript events together over a narrow consumer-declared
  read-only interface (the `LostRecoveryLister`/`LeaseLister` pattern, not the
  ~570-method `Store`), with cursors per `agent_run_id`.
- **The TUI writes no engineering state.** Controls split by action, not by
  launch mode: cancel runs in-process on an operational Engine; retry and
  `NEEDS_INFO` resume spawn a detached `forge` child (they end in `resumeIssue`
  — workspace setup, rebase, coding agent, repair loop, gates, commit, PR — so
  an effective in-process run would re-enter the orchestrator the TUI is meant
  to observe); approve and answer-a-decision are tracker writes. There is no
  intent queue because the scheduler builds the DAG once and never reads the
  store again, and no durable outbox because that would make the TUI write
  state an Engine later acts on.
- **Acknowledgement is pending-until-observed.** No optimistic state; each
  frame is one-tick-consistent. Destructive-action confirmation is UI-only —
  `forge cancel` has none and should not grow one. The TUI surfaces concurrent
  failures instead of preventing them.
- **Liveness and elapsed are display-only.** `workers.last_heartbeat` (5s beat,
  15s stale) and `execution_issues.state_changed_at` are new display columns;
  loss is *displayed*, never mutated into detection.

The storage consequences (WAL, append, stable `seq`, SQL cap, dual handles)
are ADR 0030. The framework choice is Bubble Tea v2; the layout is one
interleaved, annotated, linear timeline. Both are specified in
docs/specs/live-agent-tui.md.
