package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Teagan42/forge/internal/agent"
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
// renders a sensible commit message. It renders a Conventional Commits
// header ({type}: {title}), a wrapped body describing the change, and a
// trailing issue reference (ticket 78's acceptance criteria).
const defaultCommitMessageTemplate = "{type}: {title}\n\n{body}\n\nRefs #{issue}"

// commitMessageWrapWidth is the column at which commit message and pull
// request body prose is wrapped (ticket 78's "wrap at 80 characters"
// acceptance criterion).
const commitMessageWrapWidth = 80

// conventionalCommitHeader matches a Conventional Commits header's prefix:
// type[(scope)][!]: , capturing the type (group 1) and the full prefix
// (group 0, up to and including the trailing ": ") so callers can both
// validate the type against allowedConventionalCommitTypes and strip the
// prefix when it's not one. Used to detect an Issue Title that already
// carries its own conventional-commit prefix, so commitMessage/prTitle
// don't double-prefix it.
var conventionalCommitHeader = regexp.MustCompile(`^([a-z]+)(\([a-zA-Z0-9_./-]+\))?!?: `)

// allowedConventionalCommitTypes is the set of Conventional Commits types
// this repo's CI "Conventional Commit PR title" check
// (amannn/action-semantic-pull-request, .github/workflows/ci.yml) accepts
// under its default configuration. An Issue Title carrying a prefix outside
// this set (e.g. an area prefix like "review:") is not "already
// conventional" — see issue 187 — and must have that prefix replaced by the
// inferred type rather than passed through.
var allowedConventionalCommitTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"docs":     true,
	"style":    true,
	"refactor": true,
	"perf":     true,
	"test":     true,
	"build":    true,
	"ci":       true,
	"chore":    true,
	"revert":   true,
}

// conventionalCommitTypeKeywords maps keywords that may appear in an
// Issue's Title or Body to the Conventional Commits type they imply. Checked
// in order; the first match wins. This is a best-effort heuristic — Forge's
// Issue model (CONTEXT.md "Issue") carries no explicit change-type field, so
// the type is inferred from the text an Agent/human already wrote.
var conventionalCommitTypeKeywords = []struct {
	keyword string
	ctype   string
}{
	{"fix", "fix"},
	{"bug", "fix"},
	{"doc", "docs"},
	{"refactor", "refactor"},
	{"test", "test"},
	{"perf", "perf"},
	{"chore", "chore"},
	{"cleanup", "chore"},
}

// defaultConventionalCommitType is used when no keyword in
// conventionalCommitTypeKeywords matches the Issue's Title/Body: most Forge
// Issues implement new functionality, so "feat" is the safest default.
const defaultConventionalCommitType = "feat"

const emptyDiffNeedsInfoQuestion = "The agent reported IMPLEMENTED, but there is no code diff against the worker base. Is this a legitimate no-code deliverable?"

const emptyDiffNeedsInfoContext = "Forge refuses to open an empty pull request. Confirm whether this issue should be handled as verification-only or tracker-only work, or send it back for a code change."

// runCommitAndPR implements ticket 22's COMMITTING and PR_CREATING stages,
// entered once Review approves an implementation (or, with no Reviewer
// configured, once Quality Gates pass — see runReview): first refuse an
// empty diff by routing the Issue to NEEDS_INFO before publication, then
// commit the Workspace's validated work (Publisher.Commit) with a
// configurable message template, push the branch (Publisher.Push), create
// or recover a pull request (PRTracker.CreatePullRequest, idempotent by
// head branch), persist the PR id/url and commit SHA, and transition the
// Issue through PR_CREATING to CI_PENDING.
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

	issue, guarded, err := e.guardEmptyDiff(ctx, executionID, issueID, workerBase, ws.Path, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if guarded {
		return issue, nil
	}

	summary, err := e.agentSummary(ctx, executionID, issueID)
	if err != nil {
		return domain.Issue{}, err
	}
	message := e.commitMessage(issue, summary)
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

	// The Feature may have been frozen by another Worker's REPLAN_REQUIRED
	// escalation while this one was mid-flight. Committing and pushing its
	// own branch above was safe; creating the pull request is the first step
	// that integrates against the invalidated plan, so that is where a
	// frozen Feature suspends the Worker (ticket 22, acceptance item 4).
	issue, suspended, err := e.guardReplanIntegration(ctx, executionID, issueID, issue)
	if err != nil {
		return domain.Issue{}, err
	}
	if suspended {
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
		Body:  prBody(issue, summary),
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

// guardEmptyDiff is the empty-diff pre-publication guard: before the
// Workspace is committed or pushed, it re-derives the diff against
// workerBase (the same DiffProducer seam runReview uses; production includes
// staged, unstaged, and untracked worktree changes) and, if the Agent's
// overall change set is empty, routes the Issue to NEEDS_INFO
// instead of letting runCommitAndPR publish an empty branch or PR. This
// catches an Agent that reported StatusImplemented (and, with no Reviewer
// configured, sailed straight through REVIEWING) without actually changing
// anything, while leaving room for a human to confirm legitimate no-code
// deliverables.
//
// guarded reports whether the guard tripped: true means issue has already
// been driven to NEEDS_INFO and runCommitAndPR must stop (no commit, push,
// PR_CREATING transition, or CreatePullRequest call); false means the diff
// is non-empty and runCommitAndPR should proceed as normal.
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
	issue, err = e.handleNeedsInfo(ctx, executionID, issueID, workerRef(executionID, issueID), agent.AgentResult{
		Status:  agent.StatusNeedsInfo,
		Summary: "Agent reported IMPLEMENTED, but Forge found no diff against the worker base.",
		NeedsInfo: &agent.NeedsInfoDetail{
			Question: emptyDiffNeedsInfoQuestion,
			Context:  emptyDiffNeedsInfoContext,
		},
	})
	return issue, true, err
}

// agentSummary recovers the implementing Agent's human-readable summary
// (agent.AgentResult.Summary) from the "agent.result" Event invokeAgent
// appends (see engine.go's appendEvent call), so the commit/PR message
// built well after that Agent invocation returned can still describe what
// it actually did. Empty if no "agent.result" Event was recorded (e.g. a
// test double that skips event appending) or its summary was blank.
func (e *Engine) agentSummary(ctx context.Context, executionID, issueID string) (string, error) {
	events, err := e.Store.EventsByIssue(ctx, executionID, issueID)
	if err != nil {
		return "", fmt.Errorf("engine: load events for issue %s: %w", issueID, err)
	}
	var summary string
	for _, evt := range events {
		if evt.Type != "agent.result" {
			continue
		}
		var payload struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal([]byte(evt.Data), &payload); err != nil {
			continue
		}
		if strings.TrimSpace(payload.Summary) != "" {
			summary = payload.Summary
		}
	}
	return summary, nil
}

// commitMessage renders e.Config.PullRequests.CommitMessageTemplate (or
// defaultCommitMessageTemplate, if unset) against issue's
// {type}/{title}/{body}/{issue} placeholders (ticket 78: commit messages
// must be Conventional Commits formatted, wrap at 80 columns, and carry a
// title, a body, and the issue id at the end).
func (e *Engine) commitMessage(issue domain.Issue, summary string) string {
	tmpl := e.Config.PullRequests.CommitMessageTemplate
	if tmpl == "" {
		tmpl = defaultCommitMessageTemplate
	}
	return renderIssueTemplate(tmpl, issue, summary)
}

// prTitle is the pull request's title: the Issue's Title, Conventional
// Commits-prefixed unless it already carries its own valid prefix (e.g. an
// Issue titled "fix: nil panic on empty diff"), falling back to a generic
// "Issue #<id>" label for a tracker adapter that doesn't populate Title. A
// prefix that looks conventional but isn't an allowed Conventional Commits
// type (e.g. an area prefix like "review:") is stripped and replaced by the
// inferred type rather than nested underneath it (issue 187: "feat:
// review: …" is not acceptable).
func prTitle(issue domain.Issue) string {
	if issue.Title == "" {
		issue.Title = "Issue #" + issue.ID
	}
	ctype, title := conventionalCommitHeaderParts(issue)
	return ctype + ": " + title
}

// stripInvalidConventionalCommitPrefix removes a leading "<word>: " prefix
// from title when that word is not an allowed Conventional Commits type, so
// prTitle/renderIssueTemplate can prepend the inferred type without nesting
// it under the invalid one (issue 187).
func stripInvalidConventionalCommitPrefix(title string) string {
	if loc := conventionalCommitHeader.FindStringIndex(title); loc != nil {
		return title[loc[1]:]
	}
	return title
}

// conventionalCommitType infers the Conventional Commits type implied by
// issue's Title/Body via conventionalCommitTypeKeywords, defaulting to
// defaultConventionalCommitType.
func conventionalCommitType(issue domain.Issue) string {
	haystack := strings.ToLower(issue.Title + " " + issue.Body)
	for _, kw := range conventionalCommitTypeKeywords {
		if strings.Contains(haystack, kw.keyword) {
			return kw.ctype
		}
	}
	return defaultConventionalCommitType
}

// changeDescription is the free-form prose describing what changed, shared
// by the commit body and the PR's "Why"/"What Was Changed" sections: the
// Agent's own summary of its work when available, falling back to the
// Issue's Body (its description), and finally a generic sentence so the
// commit/PR are never left with an empty section.
func changeDescription(issue domain.Issue, summary string) string {
	if strings.TrimSpace(summary) != "" {
		return strings.TrimSpace(summary)
	}
	if strings.TrimSpace(issue.Body) != "" {
		return strings.TrimSpace(issue.Body)
	}
	return fmt.Sprintf("Implements the requirements described in issue #%s.", issue.ID)
}

// prBody renders the pull request description with the sections ticket 78
// requires: Summary, Why, What Was Changed, How it Was Tested, and the
// `Closes #<number>` issue reference GitHub uses to auto-close the Issue on
// merge. Quality Gates and Review have both already passed by the time
// runCommitAndPR runs — the resting-state doc comments on
// runQualityGates/runReview establish that invariant — so "How it Was
// Tested" reports that checklist rather than re-deriving it.
func prBody(issue domain.Issue, summary string) string {
	description := wrapText(changeDescription(issue, summary), commitMessageWrapWidth)

	var b strings.Builder
	b.WriteString("## Summary\n\n")
	b.WriteString(description)
	b.WriteString("\n\n## Why\n\n")
	fmt.Fprintf(&b, "Addresses issue #%s: %s\n\n", issue.ID, prTitle(issue))
	b.WriteString("## What Was Changed\n\n")
	b.WriteString(description)
	b.WriteString("\n\n## How it Was Tested\n\n")
	b.WriteString("- [x] Quality Gates passed\n")
	b.WriteString("- [x] Review approved\n\n")
	fmt.Fprintf(&b, "Closes #%s\n", issue.ID)
	return b.String()
}

// renderIssueTemplate replaces the {type}, {title}, {body}, and {issue}
// placeholders in tmpl with the Conventional Commits type, issue.Title, a
// wrapped change description, and issue.ID respectively. {type}/{title} are
// derived the same way prTitle derives its header, so a template rendering
// "{type}: {title}" (the default) always matches prTitle's output: an
// invalid area prefix (e.g. "review:") on issue.Title is stripped from
// {title} rather than left in place, which would otherwise double-prefix
// the header once {type} is prepended (issue 187).
func renderIssueTemplate(tmpl string, issue domain.Issue, summary string) string {
	ctype, title := conventionalCommitHeaderParts(issue)
	r := strings.NewReplacer(
		"{type}", ctype,
		"{title}", title,
		"{body}", wrapText(changeDescription(issue, summary), commitMessageWrapWidth),
		"{issue}", issue.ID,
	)
	return r.Replace(tmpl)
}

// conventionalCommitHeaderParts splits issue's effective Conventional
// Commits header into a type part and a title part such that
// ctype + ": " + title reproduces prTitle(issue): when issue.Title already
// carries a valid prefix, ctype is that whole prefix (type, scope, and "!"
// marker included) so the scope/marker survive the round trip; otherwise
// ctype is the inferred type and title is issue.Title stripped of any
// invalid prefix.
func conventionalCommitHeaderParts(issue domain.Issue) (ctype, title string) {
	title = issue.Title
	if m := conventionalCommitHeader.FindStringSubmatch(title); m != nil && allowedConventionalCommitTypes[m[1]] {
		return strings.TrimSuffix(m[0], ": "), title[len(m[0]):]
	}
	return conventionalCommitType(issue), stripInvalidConventionalCommitPrefix(title)
}

// wrapText word-wraps text to width columns, preserving existing blank
// lines (paragraph breaks) rather than collapsing them, so multi-paragraph
// Agent summaries/Issue bodies keep their structure (ticket 78's "wrap at
// 80 characters" acceptance criterion).
func wrapText(text string, width int) string {
	paragraphs := strings.Split(text, "\n\n")
	wrapped := make([]string, len(paragraphs))
	for i, p := range paragraphs {
		wrapped[i] = wrapParagraph(p, width)
	}
	return strings.Join(wrapped, "\n\n")
}

// wrapParagraph word-wraps a single paragraph (no blank lines) to width
// columns.
func wrapParagraph(p string, width int) string {
	words := strings.Fields(p)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		switch {
		case i == 0:
			b.WriteString(w)
			lineLen = len(w)
		case lineLen+1+len(w) > width:
			b.WriteString("\n")
			b.WriteString(w)
			lineLen = len(w)
		default:
			b.WriteString(" ")
			b.WriteString(w)
			lineLen += 1 + len(w)
		}
	}
	return b.String()
}
