# 21 — Implementation retry loop

**What to build:** Route gate failures and review rejections back to the Worker for repair, with separate retry budgets. Gate failure → re-invoke Agent with diagnostic → rerun full gates. Review rejection → re-invoke Agent with findings → rerun full gates → re-review. Every repair reruns the complete gate set. Budget exhaustion transitions to FAILED with diagnostics preserved.

**Blocked by:** 19 — Quality gate runner; 20 — Review stage

**Status:** resolved

- [x] Gate failure re-invokes Agent with only the new diagnostic information
- [x] Review rejection re-invokes Agent with structured findings
- [x] Agent receives bounded failure context, not full history replay
- [x] Every repair reruns the full configured quality gate set
- [x] Gate retry budget decrements independently from review budget
- [x] Gate budget exhaustion transitions to FAILED
- [x] Review budget exhaustion transitions to FAILED
- [x] Workspace preserved across retries (same worktree)
- [x] All retry attempts persisted with execution history
- [x] Integration test: gate fail → retry → pass → review → done
- [x] Integration test: review reject → retry → gates → review approve → done
- [x] Integration test: budget exhaustion → FAILED
