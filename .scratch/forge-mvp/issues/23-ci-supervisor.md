# 23 — CI supervisor

**What to build:** After PR creation, poll GitHub PR checks to determine CI outcome. Required checks come from `GetMergeRequirements` (branch protection/rulesets); optional check failures are ignored. All required checks green → DONE. Any required check failed → CI_FAILED with bounded diagnostics identifying the failing check.

**Blocked by:** 14 — GitHub tracker adapter; 22 — Commit and PR creation

**Status:** ready-for-agent

- [ ] Poll GitHub PR check status at configurable interval
- [ ] Required checks determined via `GetMergeRequirements` (GitHub branch protection/rulesets)
- [ ] Fallback: explicit check list from config when `ci.required_checks.mode: explicit`
- [ ] All required checks green → transition to DONE
- [ ] Required check failure → transition to CI_FAILED
- [ ] Optional check failures do not trigger CI_FAILED
- [ ] CI_FAILED includes: failing check name, bounded failure details
- [ ] CI attempts persisted
- [ ] Pending CI does not block Scheduler from processing other issues
- [ ] Integration test: all required green → DONE
- [ ] Integration test: required failure → CI_FAILED with diagnostics
- [ ] Integration test: optional failure with required green → DONE
