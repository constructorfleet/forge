package ci

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
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

// Wait polls the pull request attached to issueID until all required checks
// succeed or one required check fails.
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

	for {
		checks, err := s.Tracker.GetPullRequestChecks(ctx, pr.Number)
		if err != nil {
			return "", fmt.Errorf("ci: poll checks for issue %s: %w", issueID, err)
		}

		status, failed := evaluateChecks(required, checks)
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
			return issue.State, nil
		case storage.CIRunStatusFailed:
			issue, err := s.Store.TransitionIssue(ctx, executionID, issueID, domain.StateCIFailed)
			if err != nil {
				return "", fmt.Errorf("ci: transition issue %s to CI_FAILED: %w", issueID, err)
			}
			return issue.State, nil
		default:
			if err := s.Sleep(ctx, s.Config.CI.PollInterval); err != nil {
				return "", err
			}
		}
	}
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

func evaluateChecks(required []string, checks []tracker.PullRequestCheck) (storage.CIRunStatus, *tracker.PullRequestCheck) {
	if len(required) == 0 {
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
	return storage.CIRunStatusPassed, nil
}

func capDetails(details string, maxBytes int) string {
	if details == "" {
		return ""
	}
	w := textcap.NewTailWriter(maxBytes)
	_, _ = w.Write([]byte(details))
	return w.String()
}
