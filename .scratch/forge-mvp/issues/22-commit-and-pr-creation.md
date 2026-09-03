# 22 — Commit and PR creation

**What to build:** After Review approval, commit validated work with a configurable template, push the branch, and create a GitHub PR with issue reference and validation summary. Idempotent — retries detect existing PRs and branches. Issue transitions to CI_PENDING.

**Blocked by:** 14 — GitHub tracker adapter; 15 — Workspace manager; 21 — Implementation retry loop

**Status:** resolved

- [x] Inspect workspace for dirty state before committing
- [x] Commit with configurable message template (default: issue title + `Refs #<number>`)
- [x] Push branch to remote
- [x] Create PR with: summary, validation checklist, issue reference (`Closes #<number>`)
- [x] PR ID and URL persisted
- [x] Issue transitions to CI_PENDING after PR creation
- [x] Idempotent: existing PR for branch is recovered, not duplicated
- [x] Idempotent: push handles existing remote branch
- [x] Commit SHA captured and recorded
- [x] Integration test: approved work → commit → push → PR created → CI_PENDING
