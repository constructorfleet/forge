# 24 — CI failure repair

**What to build:** Route CI failures back to the Worker for automated repair. Fetch failing check details, bound diagnostic output, re-invoke Agent in existing Workspace, rerun full Quality Gates, re-review, commit correction, push to existing PR, resume CI monitoring. Separate CI retry budget — exhaustion transitions to FAILED.

**Blocked by:** 21 — Implementation retry loop; 23 — CI supervisor

**Status:** resolved

- [x] CI_FAILED fetches failing check details from GitHub
- [x] Diagnostic output bounded to configured max bytes
- [x] Agent re-invoked with CI failure context only (not unrelated history)
- [x] Repair reruns full quality gate set
- [x] Repair goes through review stage
- [x] Corrective commit pushed to existing PR branch
- [x] CI monitoring resumes after push (CI_PENDING)
- [x] CI retry budget decrements independently from gate/review budgets
- [x] CI budget exhaustion transitions to FAILED with diagnostics preserved
- [x] Integration test: CI fail → repair → gates → review → push → CI green → DONE
- [x] Integration test: CI budget exhaustion → FAILED
