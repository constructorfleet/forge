# 23 — CI supervisor

**What to build:** After PR creation, poll GitHub PR checks to determine CI outcome. Required checks come from `GetMergeRequirements` (branch protection/rulesets); optional check failures are ignored. All required checks green → DONE. Any required check failed → CI_FAILED with bounded diagnostics identifying the failing check.

**Blocked by:** 14 — GitHub tracker adapter; 22 — Commit and PR creation

**Status:** resolved

- [x] Poll GitHub PR check status at configurable interval
- [x] Required checks determined via `GetMergeRequirements` (GitHub branch protection/rulesets)
- [x] Fallback: explicit check list from config when `ci.required_checks.mode: explicit`
- [x] All required checks green → transition to DONE
- [x] Required check failure → transition to CI_FAILED
- [x] Optional check failures do not trigger CI_FAILED
- [x] CI_FAILED includes: failing check name, bounded failure details
- [x] CI attempts persisted
- [x] Pending CI does not block Scheduler from processing other issues
- [x] Integration test: all required green → DONE
- [x] Integration test: required failure → CI_FAILED with diagnostics
- [x] Integration test: optional failure with required green → DONE
