# Automatic PR conflict resolution is limited to Git-clean replay with rollback

Forge may automatically repair a merge-conflicted pull request only by asking Git to replay the pull request branch onto the current target branch in an isolated candidate Workspace. Forge must never synthesize conflict resolutions, choose "ours" or "theirs", edit conflict markers, run an Agent to interpret hunks, enable rerere-based reuse, or apply semantic guesses. If Git cannot produce a complete candidate tree without unresolved conflicts, the conflict remains a human decision and the Issue routes to `NEEDS_INFO` exactly as it does today.

This supersedes this ADR's original placeholder. The original problem statement was correct that `internal/ci/conflict.go` routed every conflict to `NEEDS_INFO`; the decision here is the narrow exception future implementation may build against.

## Decision

When the CI Supervisor detects that a Forge-managed pull request is conflicted with its base branch, it may run one automatic resolution attempt if all of these preconditions hold:

- The pull request head still matches the branch and commit Forge recorded for the Issue. A head branch moved by a human or another system after Forge's last known push is not Forge-owned state for this purpose.
- The target branch tip is known and reachable. The new target tip must be a forward movement from the base already recorded for the Worker, preserving ADR 0011's rule that base refreshes add commits and do not silently drop already-captured history.
- The Issue Workspace has no uncommitted changes and no in-progress Git operation. A dirty or half-mutated Workspace routes to `NEEDS_INFO` instead of compounding ambiguity.
- The tracker exposes enough merge/head information and Forge has a configured branch pusher that supports lease-guarded updates.

The only safe conflict shape is "Git can construct the whole candidate without stopping." Practically, the implementation should:

- Create a disposable conflict-resolution branch and Workspace from the recorded pull request head, not from the live Issue Workspace.
- Rebase that candidate onto the current target tip, or use the repository's chosen equivalent replay primitive, with rerere disabled and no custom hunk-resolution strategy supplied by Forge.
- Treat a zero-conflict Git exit as a candidate, even when Git had to perform ordinary non-overlapping three-way application internally.
- Treat any unresolved path as unsafe: overlapping textual hunks, add/add, delete/modify, rename/rename, rename/delete, mode conflicts, submodule conflicts, binary conflicts, generated-file conflicts, custom-driver failures, or any other Git stop condition all route to `NEEDS_INFO` with the path list.

Forge's definition of "trivial" is therefore operational rather than heuristic: Git completed a replay in isolation and produced a concrete tree; Forge did not choose any content. Forge does not attempt a second strategy after one strategy conflicts, because choosing between rebase, merge, squash, or hunk-level repair is itself a policy decision that can alter branch history and review context.

## Validation

A candidate is not publishable merely because Git replayed it. Before touching the pull request branch, Forge must run the same configured Quality Gates that protect a normal Worker publication. Those gates run in the candidate Workspace against the candidate branch.

If any local Quality Gate fails, Forge discards the candidate branch and candidate Workspace, records the failed automatic-resolution attempt, and routes the Issue to `NEEDS_INFO`. The live Issue Workspace, local Issue branch, remote pull request branch, and recorded pull request head remain at their pre-attempt commit.

If local Quality Gates pass, Forge may push the candidate to the pull request branch using a lease that requires the remote head to still equal the pre-attempt pull request head. A lease failure means someone else moved the branch; Forge discards the candidate and routes to `NEEDS_INFO` rather than overwriting that work.

After a successful push, normal CI supervision continues from `CI_PENDING`. If required CI or actionable review feedback fails after the automatic-resolution push, Forge must restore the pull request branch to the pre-attempt head with a second lease that requires the remote head to still equal the candidate head. After that restoration it routes the Issue to `NEEDS_INFO`, records that the candidate failed after publication, and includes both the candidate SHA and restored SHA in the durable detail. The CI retry budget is not consumed for this conflict-resolution rollback path; ADR 0007's CI budget remains for ordinary post-PR repair loops, while this path is a failed automatic conflict-resolution detour that needs human judgment.

If remote restoration cannot be performed because the branch moved again, Forge must stop immediately in `NEEDS_INFO` and report the restoration failure with the expected candidate SHA, observed remote SHA, and original SHA. It must not force-push over unexpected remote state.

## Restoration Semantics

The attempt has three durable snapshots:

- **Original:** the local Issue branch, live Issue Workspace path, recorded pull request head SHA, remote pull request head SHA, target branch tip, and Issue state before the attempt.
- **Candidate:** the disposable branch name, candidate Workspace path, candidate commit SHA, and local Quality Gate results.
- **Outcome:** whether Forge discarded the candidate without publishing, published it, restored the branch after downstream failure, or failed to restore because ownership was lost.

On success, the pull request branch and live Issue Workspace are left at the candidate commit. The disposable Workspace is removed, and the Issue remains under normal CI supervision until the pull request merges or reaches another resting state.

On any pre-push failure, the live Issue Workspace and branch are untouched. The candidate Workspace and branch are removed. The Issue transitions to `NEEDS_INFO` with a comment explaining why automatic resolution was refused or discarded.

On post-push failure, Forge restores the remote pull request branch and live Issue Workspace to the original commit before transitioning to `NEEDS_INFO`, unless the lease check proves ownership was lost. In the ownership-lost case, Forge reports the mismatch and leaves the remote branch untouched.

These rules intentionally match the existing conflict-adjacent invariants:

- ADR 0011: base movement is forward-only and a conflicted rebase is surfaced without partial mutation.
- ADR 0012: integration conflicts are reported with paths and partial integration state is discarded.
- `workspace.Manager.Rebase`: a conflicted rebase aborts and leaves the Workspace as it was.
- `workspace.Manager.Integrate`: a conflicted integration branch is destroyed rather than kept as a stale or partial base.

## Recording

Future implementation should make the attempt auditable rather than burying it in logs. At minimum, Forge should persist a CI/run-level record for each automatic-resolution attempt with:

- issue and execution identifiers;
- original head SHA, target tip SHA, and candidate SHA when available;
- classification: refused precondition, Git conflict, local gate failure, push lease failure, published, downstream CI/review failure, restored, or restoration lease failure;
- conflicting paths or failed gate/check details, capped the same way existing CI details are capped;
- final Issue state and whether the pull request branch was changed or restored.

The human-facing `NEEDS_INFO` comment should answer two questions: why Forge could not safely finish the automatic resolution, and what state the branch/workspace were left in. A successful automatic resolution should leave an audit event or comment that the branch was mechanically replayed and gated, without claiming that Forge semantically resolved a content conflict.

## Consequences

This design deliberately leaves many possible conflicts unresolved. That is the point. Forge may automate stale-branch mechanics, but it must not convert content judgment into unreviewed machine policy. The implementation can later broaden the safe set only by adding a new ADR or amending this one with an equally concrete restoration model.
