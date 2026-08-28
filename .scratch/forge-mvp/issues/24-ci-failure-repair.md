# 24 — CI failure repair

**What to build:** Route CI failures back to the Worker for automated repair. Fetch failing check details, bound diagnostic output, re-invoke Agent in existing Workspace, rerun full Quality Gates, re-review, commit correction, push to existing PR, resume CI monitoring. Separate CI retry budget — exhaustion transitions to FAILED.

**Blocked by:** 21 — Implementation retry loop; 23 — CI supervisor

**Status:** ready-for-agent

- [ ] CI_FAILED fetches failing check details from GitHub
- [ ] Diagnostic output bounded to configured max bytes
- [ ] Agent re-invoked with CI failure context only (not unrelated history)
- [ ] Repair reruns full quality gate set
- [ ] Repair goes through review stage
- [ ] Corrective commit pushed to existing PR branch
- [ ] CI monitoring resumes after push (CI_PENDING)
- [ ] CI retry budget decrements independently from gate/review budgets
- [ ] CI budget exhaustion transitions to FAILED with diagnostics preserved
- [ ] Integration test: CI fail → repair → gates → review → push → CI green → DONE
- [ ] Integration test: CI budget exhaustion → FAILED
