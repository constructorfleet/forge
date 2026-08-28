# 21 — Implementation retry loop

**What to build:** Route gate failures and review rejections back to the Worker for repair, with separate retry budgets. Gate failure → re-invoke Agent with diagnostic → rerun full gates. Review rejection → re-invoke Agent with findings → rerun full gates → re-review. Every repair reruns the complete gate set. Budget exhaustion transitions to FAILED with diagnostics preserved.

**Blocked by:** 19 — Quality gate runner; 20 — Review stage

**Status:** ready-for-agent

- [ ] Gate failure re-invokes Agent with only the new diagnostic information
- [ ] Review rejection re-invokes Agent with structured findings
- [ ] Agent receives bounded failure context, not full history replay
- [ ] Every repair reruns the full configured quality gate set
- [ ] Gate retry budget decrements independently from review budget
- [ ] Gate budget exhaustion transitions to FAILED
- [ ] Review budget exhaustion transitions to FAILED
- [ ] Workspace preserved across retries (same worktree)
- [ ] All retry attempts persisted with execution history
- [ ] Integration test: gate fail → retry → pass → review → done
- [ ] Integration test: review reject → retry → gates → review approve → done
- [ ] Integration test: budget exhaustion → FAILED
