# Transcript events persist as WAL-backed appends with a stable per-run seq

Transcript playback and the live TUI tail depend on a read path the original
write shape cannot serve. `TranscriptRecorder.Events()` rewrote the whole
500-event window on every emit (DELETE plus re-INSERT per incoming event),
reassigned `seq` to `0..n-1` on each read so cursors slid after eviction, and
ran without `journal_mode=WAL`, so a polling reader stalled behind a writer's
EXCLUSIVE lock. We chose to make the storage append-only instead of adding a
broker or a second in-memory event feed, because the SQLite store is already
the cross-process coordination plane (cancel, `owner_pid`).

Decisions recorded here are the ones a maintainer might otherwise "simplify"
and break:

- **`journal_mode=WAL` and `synchronous(NORMAL)` go in the DSN `_pragma` list**,
  not as a one-shot `PRAGMA` exec, so the setting is re-applied per connection
  and cannot lapse. A DSN pragma is a no-op where WAL does not exist (e.g.
  `file::memory:?cache=shared`), so memory DBs are unaffected.
- **WAL is persistent on-disk state; it does not self-revert.** Reverting the
  code leaves the database in WAL mode. This is safe here — every backend
  mounts the worktree, never `.forge/` (ADRs 0021/0022) — but it is a fact a
  future engineer must not assume away.
- **Writes become an append of only unflushed events on a ~250ms batch**, plus
  a flush at run completion. Every flush runs on `context.WithoutCancel(ctx)`,
  which the sink takes at construction (issue 454): `database/sql` rejects a
  write on an already cancelled context, so a run cancelled before it streams
  anything would otherwise persist not even its diagnostic fallback and would
  read as a blank transcript. Capture stays best-effort: a failed flush drops
  events and lets gap detection report them rather than changing the run's
  outcome.
- **`seq` becomes a stable per-run arrival ordinal assigned once at `Emit`**,
  never renumbered, with `UNIQUE (agent_run_id, seq)` making a double-write
  loud. Eviction leaves gaps; the lowest `seq` returned is the eviction count.
  Nothing in the tree reads `seq` as a value, so the redefinition is nearly
  free. Storage stops persisting `TRUNCATION` (the type stays for adapters'
  own field clipping).
- **The retention cap moves into SQL: 5000 events, delete-oldest**, enforced in
  the same transaction as an append, replacing the recorder's in-memory 500
  that bound reattach backfill.
- **Two database handles:** a normal read-write handle (`*sql.DB`) for writers
  (pool capped at 1), and a second normal read-write handle used read-only by
  observers. A `mode=ro` handle cannot create the `-wal`/`-shm` sidecars WAL
  needs.

Sized as part of the live-agent TUI effort; see docs/specs/live-agent-tui.md.
