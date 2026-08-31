package ci

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/statusreflect"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/textcap"
	"github.com/Teagan42/forge/internal/tracker"
)

// Tracker is the subset of tracker.Tracker the CI supervisor needs.
type Tracker interface {
	GetMergeRequirements(ctx context.Context, branch string) (tracker.MergeRequirements, error)
	GetPullRequestChecks(ctx context.Context, number int) ([]tracker.PullRequestCheck, error)
}

// Sleeper abstracts waiting between CI polls for deterministic tests.
type Sleeper func(ctx context.Context, d time.Duration) error

// Supervisor monitors pull-request checks for one Issue already in
// CI_PENDING and transitions it to DONE or CI_FAILED.
type Supervisor struct {
	Store      storage.Store
	Tracker    Tracker
	Config     config.Config
	BaseBranch string
	Now        func() time.Time
	Sleep      Sleeper

	// StatusTracker is the subset of tracker.Tracker the ticket-24
	// status-reflection signal uses (internal/statusreflect) to swap the
	// in-progress label for the in-review label (or clear it entirely) once
	// Wait reaches a resting state. Optional: nil (or
	// Config.StatusReflection.Enabled false, the default) leaves Wait's
	// tracker side effect a no-op, matching every other optional seam in
	// this codebase (see engine.Engine.StatusTracker).
	StatusTracker statusreflect.Tracker

	// NeedsInfoTracker is the subset of tracker.Tracker Wait uses to add
	// the configured Config.Blocked.Label and post a structured comment
	// when it routes an unresolvable merge conflict or ambiguous review
	// feedback to NEEDS_INFO (issue 109). Optional like StatusTracker: nil
	// (or an unset Config.Blocked.Label) leaves the label/comment side
	// effects a no-op — Wait still transitions the Issue either way, same
	// as engine.Engine's handleNeedsInfo does for the IMPLEMENTING-side
	// NEEDS_INFO path.
	NeedsInfoTracker NeedsInfoTracker

	// Rebaser is the workspace-level capability Wait uses to move a stale
	// pull request's Workspace branch onto BaseBranch (issue 233). Optional
	// like StatusTracker/NeedsInfoTracker: nil leaves Wait's staleness poll
	// a no-op, so existing callers of New keep compiling and behaving
	// unchanged until cmd/forge wires a production implementation.
	Rebaser Rebaser

	// Pusher force-pushes a rebased Workspace branch back to its tracker
	// remote once Rebaser has moved it (issue 233). Optional alongside
	// Rebaser: both must be set for Wait to attempt automatic staleness
	// remediation.
	Pusher BranchPusher
}

// NeedsInfoTracker is the subset of tracker.Tracker Wait needs when routing
// an unresolvable merge conflict or ambiguous review feedback to
// NEEDS_INFO. Structurally identical to engine.NeedsInfoTracker; duplicated
// narrowly here so internal/ci does not import internal/engine (Engine
// itself depends on internal/ci via the CIWaiter interface, so the reverse
// import would cycle).
type NeedsInfoTracker interface {
	AddLabel(ctx context.Context, id string, label string) error
	AddComment(ctx context.Context, id string, body string) (tracker.Comment, error)
}

func New(store storage.Store, trk Tracker, cfg config.Config, baseBranch string) *Supervisor {
	return &Supervisor{
		Store:      store,
		Tracker:    trk,
		Config:     cfg,
		BaseBranch: baseBranch,
		Now:        time.Now,
		Sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

// checksSettleGracePolls is how many consecutive polls must observe the
// same set of reported PR check names before Wait trusts that set as
// complete when it has no tracker-declared required checks to fall back
// on. A single poll's check list is not enough evidence either when it is
// empty (GitHub takes a few seconds after PR/commit creation to register
// its first check run, issue 215) or when it is non-empty (a PR's checks
// can register across more than one workflow, so an early poll may see
// only a fast job and miss a slower one that hasn't reported in yet at
// all, issue 231). Requiring the observed set to hold steady across a poll
// interval rules both races out without a fixed grace delay.
const checksSettleGracePolls = 2

// Wait polls the pull request attached to issueID until the pull request is
// merged after all required checks succeed, or one required check fails.
func (s *Supervisor) Wait(ctx context.Context, executionID, issueID string) (domain.IssueState, error) {
	required, err := s.requiredChecks(ctx)
	if err != nil {
		return "", fmt.Errorf("ci: determine required checks for issue %s: %w", issueID, err)
	}

	prs, err := s.Store.PullRequestsByIssue(ctx, executionID, issueID)
	if err != nil {
		return "", fmt.Errorf("ci: load pull requests for issue %s: %w", issueID, err)
	}
	if len(prs) == 0 {
		return "", fmt.Errorf("ci: issue %s has no recorded pull request", issueID)
	}
	pr := prs[len(prs)-1]

	var lastCheckNames []string
	haveLastCheckNames := false
	settledStreak := 0
	for {
		// Mergeability, staleness, and review feedback are checked ahead of
		// required checks each poll (issue 109, "Merge Conflicts" / "Review
		// Rectification"; issue 233, staleness): all are optional Tracker
		// (and, for staleness remediation, Rebaser/Pusher) capabilities (see
		// tracker.MergeStatusGetter/ReviewsGetter), so a Tracker/Supervisor
		// that doesn't implement/configure them (including every existing
		// test double) leaves this exactly the pre-issue-109 check-only
		// behavior.
		if handled, state, err := s.pollConflict(ctx, executionID, issueID, pr.Number); handled || err != nil {
			return state, err
		}
		if handled, state, err := s.pollStale(ctx, executionID, issueID, pr.Number); handled || err != nil {
			return state, err
		}
		if handled, state, err := s.pollReviews(ctx, executionID, issueID, pr.Number); handled || err != nil {
			return state, err
		}

		checks, err := s.Tracker.GetPullRequestChecks(ctx, pr.Number)
		if err != nil {
			return "", fmt.Errorf("ci: poll checks for issue %s: %w", issueID, err)
		}

		currentCheckNames := checkNames(checks)
		if haveLastCheckNames && slices.Equal(currentCheckNames, lastCheckNames) {
			settledStreak++
		} else {
			settledStreak = 1
		}
		lastCheckNames = currentCheckNames
		haveLastCheckNames = true
		status, failed := evaluateChecks(required, checks, settledStreak >= checksSettleGracePolls)
		if status == storage.CIRunStatusPassed {
			merged, err := s.pullRequestMerged(ctx, issueID, pr.Number)
			if err != nil {
				return "", err
			}
			if !merged {
				status = storage.CIRunStatusPending
			}
		}
		run := storage.CIRun{
			ExecutionID: executionID,
			IssueID:     issueID,
			Status:      status,
			CheckedAt:   s.Now(),
		}
		if failed != nil {
			run.CheckName = failed.Name
			run.Details = capDetails(failed.Details, s.Config.CI.MaxOutputBytes)
		}
		if err := s.Store.RecordCIRun(ctx, run); err != nil {
			return "", fmt.Errorf("ci: persist run for issue %s: %w", issueID, err)
		}

		switch status {
		case storage.CIRunStatusPassed:
			issue, err := s.Store.TransitionIssue(ctx, executionID, issueID, domain.StateDone)
			if err != nil {
				return "", fmt.Errorf("ci: transition issue %s to DONE: %w", issueID, err)
			}
			if err := statusreflect.Apply(ctx, s.StatusTracker, s.Config.StatusReflection, issueID, domain.StateCIPending, domain.StateDone); err != nil {
				return "", fmt.Errorf("ci: reflect status for issue %s: %w", issueID, err)
			}
			return issue.State, nil
		case storage.CIRunStatusFailed:
			issue, err := s.Store.TransitionIssue(ctx, executionID, issueID, domain.StateCIFailed)
			if err != nil {
				return "", fmt.Errorf("ci: transition issue %s to CI_FAILED: %w", issueID, err)
			}
			if err := statusreflect.Apply(ctx, s.StatusTracker, s.Config.StatusReflection, issueID, domain.StateCIPending, domain.StateCIFailed); err != nil {
				return "", fmt.Errorf("ci: reflect status for issue %s: %w", issueID, err)
			}
			return issue.State, nil
		default:
			if err := s.Sleep(ctx, s.Config.CI.PollInterval); err != nil {
				return "", err
			}
		}
	}
}

func (s *Supervisor) pullRequestMerged(ctx context.Context, issueID string, number int) (bool, error) {
	getter, ok := s.Tracker.(tracker.MergeStatusGetter)
	if !ok {
		return true, nil
	}
	status, err := getter.GetPullRequestMergeStatus(ctx, number)
	if err != nil {
		return false, fmt.Errorf("ci: poll merge status for issue %s: %w", issueID, err)
	}
	return status.Merged, nil
}

func (s *Supervisor) requiredChecks(ctx context.Context) ([]string, error) {
	if s.Config.CI.MergeRequirements.Mode == config.MergeRequirementsExplicit {
		return append([]string(nil), s.Config.CI.MergeRequirements.Checks...), nil
	}
	mr, err := s.Tracker.GetMergeRequirements(ctx, s.BaseBranch)
	if err != nil {
		return nil, err
	}
	return mr.RequiredChecks, nil
}

func evaluateChecks(required []string, checks []tracker.PullRequestCheck, checksSettled bool) (storage.CIRunStatus, *tracker.PullRequestCheck) {
	usingObservedFallback := len(required) == 0
	if usingObservedFallback {
		// No tracker-declared required checks (issue 215: most commonly an
		// unprotected branch — see
		// tracker/github.Client.GetMergeRequirements) does not mean nothing
		// is running. Fall back to waiting on every check the tracker
		// currently reports for the PR; only a PR with no observed checks
		// at all has genuinely nothing to wait for.
		required = checkNames(checks)
	}
	if len(required) == 0 {
		if !checksSettled {
			// Zero required checks and zero observed checks on this poll is
			// ambiguous: it may mean no CI is configured, or it may mean CI
			// hasn't registered its first check run yet (issue 215). Treat
			// it as still pending until the caller has seen this hold
			// across checksSettleGracePolls consecutive polls.
			return storage.CIRunStatusPending, nil
		}
		return storage.CIRunStatusPassed, nil
	}
	for _, name := range required {
		idx := slices.IndexFunc(checks, func(check tracker.PullRequestCheck) bool {
			return check.Name == name
		})
		if idx == -1 {
			return storage.CIRunStatusPending, nil
		}
		switch checks[idx].State {
		case tracker.CheckSuccess:
			continue
		case tracker.CheckFailure:
			failed := checks[idx]
			return storage.CIRunStatusFailed, &failed
		default:
			return storage.CIRunStatusPending, nil
		}
	}
	if usingObservedFallback && !checksSettled {
		// Every check the tracker currently reports is green, but under
		// the observed-fallback (no tracker-declared required checks) that
		// set can still grow: a PR's checks can register across more than
		// one workflow, so an early poll may see only a fast job and miss
		// a slower one that hasn't reported in at all yet (issue 231).
		// Wait until the observed set has held steady across
		// checksSettleGracePolls consecutive polls before trusting it as
		// the complete set.
		return storage.CIRunStatusPending, nil
	}
	return storage.CIRunStatusPassed, nil
}

func checkNames(checks []tracker.PullRequestCheck) []string {
	if len(checks) == 0 {
		return nil
	}
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}
	slices.Sort(names)
	return names
}

func capDetails(details string, maxBytes int) string {
	if details == "" {
		return ""
	}
	w := textcap.NewTailWriter(maxBytes)
	_, _ = w.Write([]byte(details))
	return w.String()
}
