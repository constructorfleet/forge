# 19 — Quality gate runner

**What to build:** After the Agent returns IMPLEMENTED, run configured Quality Gates in order. Each gate executes as a subprocess, capturing name, command, start/end time, exit code, stdout, and stderr. On failure, subsequent gates stop by default. Passing gates advance state to REVIEWING. Failing gates produce bounded diagnostic feedback for the Agent.

**Blocked by:** 12 — Configuration loading and validation; 18 — Single-issue execution engine

**Status:** ready-for-agent

- [ ] Gates execute in configured order
- [ ] Each gate records: name, command, timing, exit code, stdout, stderr
- [ ] Passing all gates transitions to REVIEWING (or next pipeline stage)
- [ ] First gate failure stops subsequent gates by default
- [ ] Agent feedback includes: failing gate name, command, exit code, bounded relevant output
- [ ] Output bounded to configured max bytes
- [ ] Gate results persisted in SQLite
- [ ] Integration test: fake agent IMPLEMENTED → gates pass → state advances
- [ ] Integration test: fake agent IMPLEMENTED → gate fails → state reflects failure with diagnostic
