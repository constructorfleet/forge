package ci

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/storage"
)

type ConflictCandidate = domain.ConflictCandidate

// ConflictCandidateManager creates, rebases, and discards isolated
// conflict-repair candidates. It must not mutate the live Issue Workspace.
type ConflictCandidateManager interface {
	CreateConflictCandidate(ctx context.Context, executionID, issueID, originalSHA string) (ConflictCandidate, error)
	RebaseConflictCandidate(ctx context.Context, candidate ConflictCandidate, baseBranch string) (conflictPaths []string, err error)
	ConflictCandidateHead(ctx context.Context, candidate ConflictCandidate) (string, error)
	CleanupConflictCandidate(ctx context.Context, candidate ConflictCandidate) error
}

// ConflictBranchPusher publishes a conflict-repair candidate only if the
// remote pull-request branch still points at the recorded pre-attempt head.
type ConflictBranchPusher interface {
	EnsureWorkspaceReady(ctx context.Context, workspacePath string) error
	ForcePushWithLease(ctx context.Context, workspacePath, branch, expectedRemoteSHA string) error
}

// WorkspaceConflictResolver implements automatic PR conflict repair using a
// disposable candidate Workspace and explicit push lease.
type WorkspaceConflictResolver struct {
	store      storage.Store
	candidates ConflictCandidateManager
	pusher     ConflictBranchPusher
	now        func() time.Time
	gates      gate.CommandRunner
	config     config.Config
}

func NewWorkspaceConflictResolver(store storage.Store, candidates ConflictCandidateManager, pusher ConflictBranchPusher, now func() time.Time, gates gate.CommandRunner, cfg config.Config) *WorkspaceConflictResolver {
	if now == nil {
		now = time.Now
	}
	return &WorkspaceConflictResolver{
		store:      store,
		candidates: candidates,
		pusher:     pusher,
		now:        now,
		gates:      gates,
		config:     cfg,
	}
}

func (r *WorkspaceConflictResolver) ResolveMergeConflict(ctx context.Context, req ConflictResolutionRequest) (ConflictResolutionResult, error) {
	if req.PullRequestHeadSHA == "" {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: recorded pull request head SHA is empty",
		}, nil
	}
	if r.candidates == nil {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: no conflict candidate manager is available",
		}, nil
	}
	if r.pusher == nil {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: no conflict branch pusher is available",
		}, nil
	}

	ws, err := r.store.WorkspaceByIssue(ctx, req.ExecutionID, req.IssueID)
	if err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("load workspace: %w", err)
	}
	if err := r.pusher.EnsureWorkspaceReady(ctx, ws.Path); err != nil {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: live workspace is not ready: " + err.Error(),
		}, nil
	}

	candidate, err := r.candidates.CreateConflictCandidate(ctx, req.ExecutionID, req.IssueID, req.PullRequestHeadSHA)
	if err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("create conflict candidate: %w", err)
	}
	defer func() { _ = r.candidates.CleanupConflictCandidate(ctx, candidate) }()

	conflicts, err := r.candidates.RebaseConflictCandidate(ctx, candidate, req.BaseBranch)
	if err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("rebase onto %s: %w", req.BaseBranch, err)
	}
	if len(conflicts) > 0 {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: rebase onto " + req.BaseBranch + " conflicted: " + strings.Join(conflicts, ", "),
		}, nil
	}
	candidateHead, err := r.candidates.ConflictCandidateHead(ctx, candidate)
	if err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("read conflict candidate head: %w", err)
	}
	candidate.HeadSHA = candidateHead

	if len(r.config.Quality.Gates) > 0 && r.gates == nil {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: quality gates are configured but no gate runner is available",
		}, nil
	}

	results := gate.NewRunner(r.gates).Run(ctx, candidate.Path, r.config.Quality.Gates, gate.Options{
		MaxOutputBytes: r.config.Quality.MaxOutputBytes,
	})
	for i := range results {
		res := results[i]
		if err := r.store.RecordGateRun(ctx, storage.GateRun{
			ExecutionID: req.ExecutionID,
			IssueID:     req.IssueID,
			Name:        res.Name,
			Command:     res.Command,
			StartedAt:   res.StartedAt,
			FinishedAt:  res.FinishedAt,
			ExitCode:    res.ExitCode,
			Stdout:      res.Stdout,
			Stderr:      res.Stderr,
			Passed:      res.Passed,
		}); err != nil {
			return ConflictResolutionResult{}, fmt.Errorf("record gate run %s: %w", res.Name, err)
		}
		if !res.Passed {
			return ConflictResolutionResult{
				Resolved: false,
				Details:  fmt.Sprintf("automatic conflict replay refused: quality gate %s failed with exit code %d", res.Name, res.ExitCode),
			}, nil
		}
	}

	if err := r.pusher.ForcePushWithLease(ctx, candidate.Path, ws.Branch, req.PullRequestHeadSHA); err != nil {
		return ConflictResolutionResult{
			Resolved: false,
			Details:  "automatic conflict replay refused: force-push lease failed: " + err.Error(),
		}, nil
	}

	now := r.now()
	if err := r.store.RecordConflictResolutionAttempt(ctx, storage.ConflictResolutionAttempt{
		ExecutionID:  req.ExecutionID,
		IssueID:      req.IssueID,
		PRNumber:     req.PullRequestNumber,
		Branch:       ws.Branch,
		OriginalSHA:  req.PullRequestHeadSHA,
		CandidateSHA: candidate.HeadSHA,
		Status:       storage.ConflictResolutionStatusPublished,
		Details:      "published automatic conflict replay candidate",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return ConflictResolutionResult{}, fmt.Errorf("record conflict resolution attempt: %w", err)
	}

	details := "rebased candidate onto " + req.BaseBranch + ", quality gates passed, and force-pushed branch with recorded-head lease"
	if len(r.config.Quality.Gates) == 0 {
		details = "rebased candidate onto " + req.BaseBranch + " and force-pushed branch with recorded-head lease; no quality gates configured"
	}
	return ConflictResolutionResult{Resolved: true, Details: details}, nil
}
