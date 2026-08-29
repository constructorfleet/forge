package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
)

// Publisher commits and pushes a Workspace's validated work once Review
// approves it (CONTEXT.md "COMMITTING"). Engine stays git-free (see this
// package's doc comment): a Publisher seam lets cmd/forge implement this
// with git while tests inject a fake, exactly as DiffProducer/
// WorkspaceCreator do for their own git-backed operations.
type Publisher interface {
	// Commit inspects the Workspace for uncommitted changes and, if any
	// exist, commits them with message. It returns the resulting HEAD
	// commit SHA. A Workspace with nothing to commit (e.g. a retried run
	// resuming after a prior successful commit) is not an error — Commit
	// is then a no-op and simply returns the current HEAD SHA.
	Commit(ctx context.Context, workspacePath, message string) (sha string, err error)

	// Push pushes branch to the Workspace's remote, creating it there if
	// it does not already exist. Idempotent: pushing a branch whose remote
	// tip already matches the local branch is not an error.
	Push(ctx context.Context, workspacePath, branch string) error
}

// PRCreator is the subset of tracker.Tracker the PR_CREATING stage needs:
// idempotently creating a pull request. Depending on this narrow interface
// rather than tracker.Tracker keeps the stage backend-agnostic and its test
// double down to one method (see NeedsInfoTracker's doc comment for the
// same rationale).
type PRCreator interface {
	CreatePullRequest(ctx context.Context, req tracker.PullRequestRequest) (tracker.PullRequest, error)
}

// defaultCommitMessageTemplate mirrors
// config.PullRequestsConfig.CommitMessageTemplate's own default. Engine
// falls back to this literal (rather than requiring every caller to build
// its Config via config.Default()) so a zero-value config.Config still
// renders a sensible commit message.
const defaultCommitMessageTemplate = "{title}\n\nRefs #{issue}"

// runCommitAndPR implements ticket 22's COMMITTING and PR_CREATING stages,
// entered once Review approves an implementation (or, with no Reviewer
// configured, once Quality Gates pass — see runReview): commit the
// Workspace's validated work (Publisher.Commit) with a configurable
// message template, push the branch (Publisher.Push), guard against an
// empty diff (see guardEmptyDiff), create or recover a pull request
// (PRTracker.CreatePullRequest, idempotent by head branch), persist the PR
// id/url and commit SHA, and transition the Issue through PR_CREATING to
// CI_PENDING.
//
// Both Publisher and PRTracker are optional seams, like Reviewer/Diff
// before them (see Engine.Reviewer's doc comment): nil leaves COMMITTING a
// resting state — this ticket's predecessor behavior — so existing callers
// of New keep compiling and behaving unchanged until cmd/forge wires
// production implementations.
func (e *Engine) runCommitAndPR(ctx context.Context, executionID, issueID, workerBase string, ws domain.Workspace, issue domain.Issue) (domain.Issue, error) {
	if e.Publisher == nil || e.PRTracker == nil {
		return issue, nil
	}

	message := e.commitMessage(issue)
	sha, err := e.Publisher.Commit(ctx, ws.Path, message)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: commit issue %s: %w", issueID, err)
	}
	if err := e.appendEvent(ctx, executionID, issueID, "commit.created", map[string]string{
		"sha": sha,
	}); err != nil {
		return domain.Issue{}, err
	}

	if err := e.Publisher.Push(ctx, ws.Path, ws.Branch); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: push branch %s for issue %s: %w", ws.Branch, issueID, err)
	}
	if err := e.appendEvent(ctx, executionID, issueID, "branch.pushed", map[string]string{
		"branch": ws.Branch,
	}); err != nil {
		return domain.Issue{}, err
	}

	issue, guarded, err := e.guardEmptyDiff(ctx, executionID, issueID, workerBase, ws.Path, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if guarded {
		return issue, nil
	}

	issue, err = e.transition(ctx, executionID, issueID, domain.StatePRCreating)
	if err != nil {
		return domain.Issue{}, err
	}

	pr, err := e.PRTracker.CreatePullRequest(ctx, tracker.PullRequestRequest{
		Base:  e.BaseBranch,
		Head:  ws.Branch,
		Title: prTitle(issue),
		Body:  prBody(issue),
	})
	if err != nil {
		return domain.Issue{}, fmt.Errorf("engine: create pull request for issue %s: %w", issueID, err)
	}

	if err := e.Store.RecordPullRequest(ctx, storage.PullRequest{
		ExecutionID: executionID,
		IssueID:     issueID,
		Number:      pr.Number,
		URL:         pr.URL,
		CommitSHA:   sha,
		CreatedAt:   e.Now(),
	}); err != nil {
		return domain.Issue{}, fmt.Errorf("engine: persist pull request for issue %s: %w", issueID, err)
	}

	return e.transition(ctx, executionID, issueID, domain.StateCIPending)
}

// guardEmptyDiff is the empty-diff pre-PR guard: after the Workspace is
// committed and pushed, it re-derives the diff against workerBase (the same
// DiffProducer seam runReview uses) and, if the Agent's overall change set
// is empty, fails the Issue instead of letting runCommitAndPR open a
// pull request with nothing in it. This catches an Agent that reported
// StatusImplemented (and, with no Reviewer configured, sailed straight
// through REVIEWING) without actually changing anything.
//
// guarded reports whether the guard tripped: true means issue has already
// been driven to FAILED and runCommitAndPR must stop (no PR_CREATING
// transition, no CreatePullRequest call); false means the diff is
// non-empty and runCommitAndPR should proceed as normal.
func (e *Engine) guardEmptyDiff(ctx context.Context, executionID, issueID, workerBase, workspacePath string, issue domain.Issue) (_ domain.Issue, guarded bool, _ error) {
	if e.Diff == nil {
		return domain.Issue{}, false, fmt.Errorf("engine: Publisher is set but Diff (DiffProducer) is nil for issue %s", issueID)
	}

	diff, err := e.Diff.Diff(ctx, workspacePath, workerBase)
	if err != nil {
		return domain.Issue{}, false, fmt.Errorf("engine: produce diff for issue %s: %w", issueID, err)
	}
	if strings.TrimSpace(diff) != "" {
		return issue, false, nil
	}

	if err := e.appendEvent(ctx, executionID, issueID, "pr.empty_diff_guard", map[string]string{
		"reason": "agent reported implemented work but the diff against the worker base is empty",
	}); err != nil {
		return domain.Issue{}, false, err
	}
	issue, err = e.transition(ctx, executionID, issueID, domain.StateFailed)
	return issue, true, err
}

// commitMessage renders e.Config.PullRequests.CommitMessageTemplate (or
// defaultCommitMessageTemplate, if unset) against issue's {title}/{issue}
// placeholders.
func (e *Engine) commitMessage(issue domain.Issue) string {
	tmpl := e.Config.PullRequests.CommitMessageTemplate
	if tmpl == "" {
		tmpl = defaultCommitMessageTemplate
	}
	return renderIssueTemplate(tmpl, issue)
}

// prTitle is the pull request's title: the Issue's Title, falling back to
// a generic "Issue #<id>" label for a tracker adapter that doesn't
// populate Title.
func prTitle(issue domain.Issue) string {
	if issue.Title != "" {
		return issue.Title
	}
	return "Issue #" + issue.ID
}

// prBody renders the pull request description: a one-line summary, a
// validation checklist (Quality Gates and Review both already passed by
// the time runCommitAndPR runs — the resting-state doc comments on
// runQualityGates/runReview establish that invariant), and the `Closes
// #<number>` issue reference GitHub uses to auto-close the Issue on merge.
func prBody(issue domain.Issue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Implements #%s: %s\n\n", issue.ID, prTitle(issue))
	b.WriteString("## Validation\n\n")
	b.WriteString("- [x] Quality Gates passed\n")
	b.WriteString("- [x] Review approved\n\n")
	fmt.Fprintf(&b, "Closes #%s\n", issue.ID)
	return b.String()
}

// renderIssueTemplate replaces the {title} and {issue} placeholders in
// tmpl with issue.Title and issue.ID respectively.
func renderIssueTemplate(tmpl string, issue domain.Issue) string {
	r := strings.NewReplacer("{title}", issue.Title, "{issue}", issue.ID)
	return r.Replace(tmpl)
}
