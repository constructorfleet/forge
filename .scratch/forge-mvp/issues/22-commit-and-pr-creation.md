# 22 — Commit and PR creation

**What to build:** After Review approval, commit validated work with a configurable template, push the branch, and create a GitHub PR with issue reference and validation summary. Idempotent — retries detect existing PRs and branches. Issue transitions to CI_PENDING.

**Blocked by:** 14 — GitHub tracker adapter; 15 — Workspace manager; 21 — Implementation retry loop

**Status:** ready-for-agent

- [ ] Inspect workspace for dirty state before committing
- [ ] Commit with configurable message template (default: issue title + `Refs #<number>`)
- [ ] Push branch to remote
- [ ] Create PR with: summary, validation checklist, issue reference (`Closes #<number>`)
- [ ] PR ID and URL persisted
- [ ] Issue transitions to CI_PENDING after PR creation
- [ ] Idempotent: existing PR for branch is recovered, not duplicated
- [ ] Idempotent: push handles existing remote branch
- [ ] Commit SHA captured and recorded
- [ ] Integration test: approved work → commit → push → PR created → CI_PENDING
