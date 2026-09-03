# 11 — Project skeleton, domain model, and state machine

**What to build:** Go module with CLI scaffold (`forge` prints help), domain types for Execution, Issue, Dependency, Worker, and Workspace, and a state machine with validated transitions. Illegal state transitions return explicit errors. The domain package has no GitHub, Git, Claude, or SQLite dependencies.

**Blocked by:** None — can start immediately

**Status:** resolved

- [x] Go module initializes and builds
- [x] CLI executable runs and prints help
- [x] Domain types defined: Execution, Issue, Dependency, Worker, Workspace
- [x] Issue state enum covers all 16 states (PENDING through CANCELLED)
- [x] State transition validation accepts legal transitions and rejects illegal ones with explicit errors
- [x] External Issue scope type distinguishes managed vs. external issues
- [x] Retry budget type with separate gate, review, and CI counters
- [x] Domain package has zero infrastructure dependencies (no GitHub, Git, Claude, SQLite imports)
- [x] Unit tests cover all legal state transitions
- [x] Unit tests cover representative illegal transitions
