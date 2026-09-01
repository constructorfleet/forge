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

	// Resetter moves a restacked Workspace branch back to the dependent's
	// last published pull-request commit when the force-push of that branch
	// fails (docs/adr/0018, stacked-branch maintenance ticket 4). A failed
	// push leaves the local branch on commits the pull request does not
	// show, so Forge restores the branch before it asks a human for help.
	// Optional alongside Rebaser/Pusher: nil keeps the restack behavior, but
	// Forge then reports that it cannot restore the workspace.
	Resetter BranchResetter

	// ConflictResolver attempts the narrow automatic pull-request conflict
	// repair allowed by ADR 0017. Optional: nil preserves the conservative
	// issue-109 behavior of routing merge conflicts to NEEDS_INFO.
	ConflictResolver ConflictResolver

	// ConflictRestorer restores a published automatic conflict-repair
	// candidate when required CI or actionable review feedback fails after
	// publication. Optional: without it, Wait cannot safely continue the
	// automatic-resolution detour and routes to NEEDS_INFO with the attempt
	// left published.
	ConflictRestorer ConflictBranchRestorer
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
		mergeStatus, haveMergeStatus, err := s.mergeStatus(ctx, issueID, pr.Number)
		if err != nil {
			return "", err
		}
		if handled, state, err := s.pollConflict(ctx, executionID, issueID, pr, mergeStatus, haveMergeStatus); handled || err != nil {
			return state, err
		}
		if handled, state, err := s.pollStale(ctx, executionID, issueID, pr.Number, mergeStatus, haveMergeStatus); handled || err != nil {
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
		checksSettled := settledStreak >= checksSettleGracePolls

		status, failed := s.evaluateMergeEligibility(required, checks, checksSettled, mergeStatus, haveMergeStatus)
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
			if err := s.restackDependents(ctx, executionID, issueID); err != nil {
				return "", err
			}
			if err := statusreflect.Apply(ctx, s.StatusTracker, s.Config.StatusReflection, issueID, domain.StateCIPending, domain.StateDone); err != nil {
				return "", fmt.Errorf("ci: reflect status for issue %s: %w", issueID, err)
			}
			return issue.State, nil
		case storage.CIRunStatusFailed:
			if handled, state, err := s.handlePublishedConflictCandidateFailure(ctx, executionID, issueID, "required check "+failed.Name+" failed: "+failed.Details); handled || err != nil {
				return state, err
			}
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

// evaluateMergeEligibility resolves the required-check set (falling back to
// whatever the tracker currently observes, and waiting out the
// checksSettleGracePolls race exactly as before — both polling-specific
// concerns that stay local to internal/ci) and then runs the CI Supervisor's
// merge decision through tracker.EvaluateMergeEligibility, the composed,
// provider-neutral verdict generalized from CI's checks/requirements slice
// (issue #284/#296). mergeStatus/haveMergeStatus is the single SCM merge-
// status fetch Wait already made this poll (see Supervisor.mergeStatus) —
// evaluateMergeEligibility does not fetch it again.
func (s *Supervisor) evaluateMergeEligibility(required []string, checks []tracker.PullRequestCheck, checksSettled bool, mergeStatus tracker.PullRequestMergeStatus, haveMergeStatus bool) (storage.CIRunStatus, *tracker.PullRequestCheck) {
	usingObservedFallback := len(required) == 0
	effectiveRequired := required
	if usingObservedFallback {
		effectiveRequired = checkNames(checks)
	}

	checksBlocker, failed := classifyRequiredChecks(effectiveRequired, checks)

	if checksBlocker == nil && usingObservedFallback && !checksSettled {
		// No tracker-declared required checks (issue 215) and, under the
		// observed-fallback, the checks set can still grow (issue 231): wait
		// for it to hold steady before trusting "all currently green" as
		// complete.
		return storage.CIRunStatusPending, nil
	}

	// Conflict/Behind are deliberately not carried into this composition:
	// pollConflict/pollStale already own those gates ahead of this point in
	// Wait's loop, each with its own optional-capability/collaborator
	// degradation (e.g. Behind with no Rebaser configured is a permanent,
	// intentional no-op — see staleness.go). Re-checking them here would
	// contradict that degradation and could block forever on a signal Wait
	// has already decided not to act on this poll. Leaving MergeEligibilityInput's
	// SCM slice (MergeStatus) unwired is therefore correct for GitHub, not an
	// omission — that slice is the provider-general path a future SCM adapter
	// wires when it has no such upstream gate (spec #284, US10; see
	// tracker.MergeEligibilityInput).
	eligibility := tracker.EvaluateMergeEligibility(tracker.MergeEligibilityInput{
		ChecksBlocker: checksBlocker,
	})

	// Merged — "has the externally-observed merge actually happened yet" —
	// is the terminal-success signal EvaluateMergeEligibility's neutral
	// verdict does not itself model (see MergeEligibilityInput), so it is
	// checked directly here rather than threaded through the composition.
	// A Tracker that doesn't report merge status at all degrades to
	// Merged: true, exactly like an absent tracker.ReviewsGetter degrades
	// review gating (internal/ci/review.go) rather than failing closed.
	merged := !haveMergeStatus || mergeStatus.Merged

	switch {
	case eligibility.Mergeable && !merged:
		return storage.CIRunStatusPending, nil
	case eligibility.Mergeable:
		return storage.CIRunStatusPassed, nil
	case checksBlocker != nil && checksBlocker.Reason == tracker.ChecksFailing:
		return storage.CIRunStatusFailed, failed
	default:
		return storage.CIRunStatusPending, nil
	}
}

// classifyRequiredChecks compares required against checks in order,
// returning the CI capability's neutral blocker (nil when every required
// check currently reports Success) and, for a ChecksFailing blocker, the
// concrete failing check for CI-run diagnostics. It stops at the first
// required check that is not a reported Success, matching the priority a
// single poll must resolve to (a later check's failure does not override an
// earlier check that is merely still pending).
func classifyRequiredChecks(required []string, checks []tracker.PullRequestCheck) (*tracker.MergeBlocker, *tracker.PullRequestCheck) {
	for _, name := range required {
		idx := slices.IndexFunc(checks, func(check tracker.PullRequestCheck) bool {
			return check.Name == name
		})
		if idx == -1 {
			return &tracker.MergeBlocker{Reason: tracker.ChecksPending, Source: tracker.CapabilityCI, RawDetail: name + ": not yet reported"}, nil
		}
		switch checks[idx].State {
		case tracker.CheckSuccess:
			continue
		case tracker.CheckFailure:
			failed := checks[idx]
			return &tracker.MergeBlocker{Reason: tracker.ChecksFailing, Source: tracker.CapabilityCI, RawDetail: failed.Name + ": " + failed.Details}, &failed
		default:
			return &tracker.MergeBlocker{Reason: tracker.ChecksPending, Source: tracker.CapabilityCI, RawDetail: checks[idx].Name + ": pending"}, nil
		}
	}
	return nil, nil
}

// mergeStatus fetches the pull request's merge status at most once per Wait
// iteration, via tracker.MergeStatusGetter, and hands the single result to
// pollConflict, pollStale, and evaluateMergeEligibility — each of which
// previously type-asserted the Tracker and fetched independently, hitting
// the same GitHub mergeability endpoint up to three times per poll. haveStatus
// is false when s.Tracker doesn't implement tracker.MergeStatusGetter, in
// which case every caller degrades exactly as it did when it fetched nothing
// itself (pollConflict/pollStale no-op; evaluateMergeEligibility treats
// Merged as true).
func (s *Supervisor) mergeStatus(ctx context.Context, issueID string, number int) (status tracker.PullRequestMergeStatus, haveStatus bool, err error) {
	getter, ok := s.Tracker.(tracker.MergeStatusGetter)
	if !ok {
		return tracker.PullRequestMergeStatus{}, false, nil
	}
	status, err = getter.GetPullRequestMergeStatus(ctx, number)
	if err != nil {
		return tracker.PullRequestMergeStatus{}, false, fmt.Errorf("ci: poll merge status for issue %s: %w", issueID, err)
	}
	return status, true, nil
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
