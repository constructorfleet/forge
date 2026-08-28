# 27 — External dependencies

**What to build:** When an Issue in the Execution set depends on an Issue outside the set, load the external Issue as an observed node in the DAG. External Issues are tracked for satisfaction but never executed. Satisfaction is determined by verifying merged code is reachable from the applicable base — closed does not equal satisfied.

**Blocked by:** 14 — GitHub tracker adapter; 26 — Multi-issue scheduling and concurrency

**Status:** resolved

- [x] Dependencies referencing issues outside the execution set are loaded as external nodes
- [x] External issues use states: EXTERNAL_PENDING, EXTERNAL_SATISFIED, EXTERNAL_INVALID
- [x] Satisfaction checked by verifying associated merged PR exists and merge commit is reachable from applicable base
- [x] Closed issues without merged PRs are EXTERNAL_INVALID, not satisfied
- [x] Managed dependents remain BLOCKED_DEPENDENCY until external prerequisites are satisfied
- [x] External issues are never added to the execution set automatically
- [x] `forge resume` re-evaluates external dependency state against current remote refs
- [x] Integration test: external dep satisfied → managed dependent unblocked
- [x] Integration test: external dep closed without merge → managed dependent stays blocked
