package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/claude"
	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/scheduler"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/github"
	"github.com/Teagan42/forge/internal/workspace"
)

// defaultConfigPath is where `forge execute`/`forge status` look for
// .forge.yaml, relative to the current working directory. Its absence is
// not an error: loadConfig falls back to config.Default().
const defaultConfigPath = ".forge.yaml"

// defaultDBPath is where the SQLite Store lives, under the same .forge/
// directory the Workspace manager uses for worktrees (see IDEATION.md
// "Workspace manager").
const defaultDBPath = ".forge/forge.db"

// loadConfig loads .forge.yaml from the current working directory, falling
// back to config.Default() when the file does not exist (the zero-config
// case Load's doc comment describes .forge.yaml itself as covering, but
// Load requires the file to be present; forge's CLI additionally tolerates
// it being entirely absent).
func loadConfig(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return config.Default(), nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("forge: load config: %w", err)
	}
	return cfg, nil
}

// openStore opens (creating if necessary) the SQLite Store at dbPath and
// brings its schema up to date.
func openStore(ctx context.Context, dbPath string) (*storage.SQLiteStore, error) {
	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("forge: create %s: %w", dir, err)
		}
	}
	store, err := storage.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("forge: open store at %s: %w", dbPath, err)
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("forge: migrate store: %w", err)
	}
	return store, nil
}

// buildEngine wires the concrete adapters (GitHub tracker, git-worktree
// Workspace manager, Agent per cfg.Agent.Provider) behind engine.Engine's
// interfaces. Engine's core flow never imports these packages directly;
// only this constructor does.
func buildEngine(store storage.Store, cfg config.Config, repoRoot string) (*engine.Engine, error) {
	trk, err := buildTracker(cfg, repoRoot)
	if err != nil {
		return nil, err
	}

	wsMgr, err := workspace.NewManager(repoRoot,
		workspace.WithWorktreeRoot(cfg.Git.WorktreeRoot),
		workspace.WithBranchTemplate(cfg.Git.BranchTemplate),
	)
	if err != nil {
		return nil, fmt.Errorf("forge: workspace manager: %w", err)
	}

	ag, err := buildAgent(cfg)
	if err != nil {
		return nil, err
	}

	eng := engine.New(store, trk, wsMgr, ag, cfg, repoRoot)
	// trk implements tracker.Tracker in full, a superset of both
	// engine.IssueFetcher (Tracker, above) and engine.NeedsInfoTracker.
	eng.NeedsInfoTracker = trk
	// eng.Diff is wired unconditionally since it is inert without a
	// Reviewer (Engine only calls it from runReview, which itself is a
	// no-op when eng.Reviewer is nil). eng.Reviewer is intentionally left
	// unset: ticket 20 only requires the review.Reviewer seam to exist and
	// be injectable, not a production-ready reviewer backend; wiring a real
	// one is deferred to a later ticket.
	eng.Diff = gitDiffProducer{}
	// eng.Publisher/eng.PRTracker (ticket 22) are wired unconditionally,
	// unlike Reviewer: Engine treats them as a single all-or-nothing seam
	// (see runCommitAndPR) and a production Publisher/PRCreator exists
	// today, so there is no "not built yet" reason to leave COMMITTING a
	// resting state the way REVIEWING was left before a production
	// Reviewer existed.
	eng.Publisher = gitPublisher{}
	// trk implements tracker.Tracker in full, a superset of engine.PRCreator.
	eng.PRTracker = trk
	eng.BaseBranch = baseBranchName(cfg.Git.Base)
	return eng, nil
}

// buildScheduler wires a *scheduler.Scheduler (ticket 26) for a `forge
// execute` invocation over issueIDs: the same tracker and Engine buildEngine
// would construct for a single Issue, adapted to scheduler.Executor via
// scheduler.Adapt, plus a completionResolver DependencyResolver and a
// BaseResolver that always re-resolves cfg.Git.Base's current tip so a
// dependency-blocked Issue that becomes ready later captures a base
// containing whatever has landed on it since (CONTEXT.md "Execution":
// Worker base captured at READY, not at Execution start).
func buildScheduler(store storage.Store, cfg config.Config, repoRoot string, issueIDs []string) (*scheduler.Scheduler, error) {
	trk, err := buildTracker(cfg, repoRoot)
	if err != nil {
		return nil, err
	}

	eng, err := buildEngine(store, cfg, repoRoot)
	if err != nil {
		return nil, err
	}

	resolver := newCompletionResolver(issueIDs, trk, cfg.Git.Base)
	base := scheduler.BaseResolverFunc(func(context.Context, string) (string, error) {
		return resolveBaseRevision(repoRoot, cfg.Git.Base)
	})

	sch := scheduler.New(trk, scheduler.Adapt(eng), resolver, base, cfg.Execution.MaxParallel)
	sch.OnComplete = resolver.onComplete
	if cfg.PullRequests.WatchCI {
		sch.CIWatcher = ci.New(store, trk, cfg, baseBranchName(cfg.Git.Base))
	}
	return sch, nil
}

// publishReadyStates are the Issue states a Dependency is considered
// satisfied at, per completionResolver below.
var publishReadyStates = map[domain.IssueState]bool{
	domain.StateReviewing:  true,
	domain.StateCommitting: true,
	domain.StatePRCreating: true,
	domain.StateCIPending:  true,
	domain.StateDone:       true,
}

// completionResolver is the scheduler.DependencyResolver `forge execute`
// uses. Ticket 22 (commit/PR creation) doesn't exist yet, so there is no
// real "PR merged" signal to check for a Managed prerequisite —
// completionResolver instead considers a Dependency on a prerequisite
// that is itself part of this `forge execute` invocation's requested Issue
// set satisfied once that prerequisite has locally reached a state at or
// beyond REVIEWING (Quality Gates passed) within this same run.
//
// A prerequisite outside the requested set is an External Issue
// (CONTEXT.md), which does have a real satisfaction signal available today
// (ticket 27): checker.CheckExternal consults GitHub for a merged,
// reachable PR. completionResolver reports a Dependency on such a
// prerequisite satisfied only once CheckExternal reports
// tracker.ExternalSatisfied; EXTERNAL_PENDING and EXTERNAL_INVALID are
// both reported unsatisfied (never an error) so a permanently-invalid
// External Issue surfaces via the scheduler's existing no-progress
// (stall) detection — reused rather than duplicated — instead of a
// special-cased error path.
type completionResolver struct {
	requested  map[string]bool
	checker    tracker.ExternalChecker
	baseBranch string

	mu        sync.Mutex
	completed map[string]domain.IssueState
	// external caches only checker's *terminal* answers (Satisfied,
	// Invalid) per External Issue ID. EXTERNAL_PENDING is deliberately
	// never cached: every Satisfied call for a still-pending External
	// Issue re-invokes checker.CheckExternal, so a poll later in the same
	// `forge execute` run — or a subsequent `forge execute`/`forge resume`
	// invocation, which always constructs a fresh completionResolver with
	// an empty cache — re-evaluates against whatever has since landed on
	// the applicable base rather than trusting a stale answer.
	external map[string]tracker.ExternalState
}

func newCompletionResolver(issueIDs []string, checker tracker.ExternalChecker, baseBranch string) *completionResolver {
	requested := make(map[string]bool, len(issueIDs))
	for _, id := range issueIDs {
		requested[id] = true
	}
	return &completionResolver{
		requested:  requested,
		checker:    checker,
		baseBranch: baseBranch,
		completed:  map[string]domain.IssueState{},
		external:   map[string]tracker.ExternalState{},
	}
}

// onComplete is wired as the Scheduler's OnComplete hook: it records each
// dispatched Issue's final state as it finishes, which Satisfied then
// consults.
func (r *completionResolver) onComplete(issueID string, state domain.IssueState, err error) {
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed[issueID] = state
}

func (r *completionResolver) Satisfied(ctx context.Context, _, dependsOnID string) (bool, error) {
	if !r.requested[dependsOnID] {
		return r.externalSatisfied(ctx, dependsOnID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.completed[dependsOnID]
	return ok && publishReadyStates[state], nil
}

// externalSatisfied resolves whether an External Issue (dependsOnID, not
// part of this run's requested set — see CONTEXT.md "External Issue") is
// EXTERNAL_SATISFIED, consulting r.checker and caching only its terminal
// answers (see the external field's doc comment).
func (r *completionResolver) externalSatisfied(ctx context.Context, dependsOnID string) (bool, error) {
	r.mu.Lock()
	if state, ok := r.external[dependsOnID]; ok {
		r.mu.Unlock()
		return state == tracker.ExternalSatisfied, nil
	}
	r.mu.Unlock()

	if r.checker == nil {
		return false, fmt.Errorf(
			"forge: external issue %s: no external dependency checker configured", dependsOnID)
	}
	state, err := r.checker.CheckExternal(ctx, dependsOnID, r.baseBranch)
	if err != nil {
		return false, fmt.Errorf("forge: check external issue %s: %w", dependsOnID, err)
	}

	if state != tracker.ExternalPending {
		r.mu.Lock()
		r.external[dependsOnID] = state
		r.mu.Unlock()
	}
	return state == tracker.ExternalSatisfied, nil
}

// buildTracker constructs the GitHub tracker.Tracker adapter for repoRoot,
// resolving the target repository from its "origin" remote. Factored out of
// buildEngine so `forge resume` (which needs the full tracker.Tracker for
// GetComments, not just engine.IssueFetcher) can build the same adapter
// without constructing a whole Engine.
func buildTracker(cfg config.Config, repoRoot string) (*github.Client, error) {
	owner, repo, err := repoFromOrigin(repoRoot)
	if err != nil {
		return nil, err
	}
	trk := github.NewClient(nil, "", owner, repo)
	trk.DependencyOverrides = cfg.Dependencies.Overrides
	// Reachability is only exercised by CheckExternal (ticket 27); wiring
	// it unconditionally here, rather than only for `forge execute`, keeps
	// buildTracker's one Client construction path fully usable by any
	// future caller that needs external-dependency satisfaction (e.g. an
	// eventual `forge resume` extension) without threading it through
	// separately.
	trk.Reachability = gitReachabilityChecker{repoRoot: repoRoot}
	return trk, nil
}

// gitReachabilityChecker is the production github.GitReachabilityChecker:
// it shells out to `git merge-base --is-ancestor` against the primary
// checkout at repoRoot, the same pattern resolveBaseRevision uses for
// `git rev-parse` (Engine never shells out to git itself; only this
// package's constructors do).
type gitReachabilityChecker struct {
	repoRoot string
}

// IsAncestor reports whether commit is an ancestor of (reachable from)
// branch's current tip. `git merge-base --is-ancestor` exits 0 for "yes", 1
// for "no" (not an error — a merged-but-not-yet-landed PR is exactly this
// case), and 128 for either operand being unresolvable — which
// `merge-base` alone gives no way to attribute to commit vs branch.
//
// IsAncestor therefore checks commit's local presence itself, first: an
// External Issue's merge commit not yet present in this checkout (e.g. it
// merged into a branch nothing here has fetched) is exactly the
// "not-yet-reachable" case CheckExternal reports as EXTERNAL_PENDING, not
// a hard error — the scheduler would otherwise abort the entire `forge
// execute` run based on incidental local fetch state (see thermos review
// of #27). Once commit is confirmed known, any remaining failure (most
// commonly an unresolvable branch) is a genuine, loud error.
func (g gitReachabilityChecker) IsAncestor(ctx context.Context, commit, branch string) (bool, error) {
	verify := exec.CommandContext(ctx, "git", "-C", g.repoRoot, "cat-file", "-e", commit+"^{commit}")
	if err := verify.Run(); err != nil {
		return false, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", g.repoRoot, "merge-base", "--is-ancestor", commit, branch)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("forge: git merge-base --is-ancestor %s %s: %w", commit, branch, err)
}

// buildAgent selects the Agent Adapter per cfg.Agent.Provider. "fake" opts
// into a FakeAgent programmed to report IMPLEMENTED, so `forge execute` is
// demoable end-to-end without a real coding backend (see ticket 18: "run it
// with the fake adapter and watch state flow"). Any other provider value
// invokes the real Claude Code CLI adapter (ticket 25).
func buildAgent(cfg config.Config) (agent.Agent, error) {
	switch cfg.Agent.Provider {
	case "fake":
		fake := agent.NewFakeAgent()
		fake.ProgramDefault(agent.AgentResult{
			Status:  agent.StatusImplemented,
			Summary: "fake agent: no-op implementation",
		})
		return fake, nil
	case "claude-code", "":
		return &claude.Adapter{}, nil
	default:
		return nil, fmt.Errorf("forge: unknown agent provider %q", cfg.Agent.Provider)
	}
}

// resolveBaseRevision resolves cfg.Git.Base (e.g. "origin/main") to a
// commit SHA in repoRoot, so Engine (which never shells out to git itself)
// receives a concrete, auditable Execution.BaseRevision.
func resolveBaseRevision(repoRoot string, base string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", base).Output()
	if err != nil {
		return "", fmt.Errorf("forge: resolve base revision %q: %w", base, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// originURLPattern matches the owner/repo out of a GitHub remote URL in
// either SSH ("git@github.com:owner/repo.git") or HTTPS
// ("https://github.com/owner/repo.git" or "https://github.com/owner/repo")
// form.
var originURLPattern = regexp.MustCompile(`github\.com[:/]([^/]+)/(.+?)(?:\.git)?$`)

// repoFromOrigin resolves the GitHub owner/repo the tracker should target
// from the primary checkout's "origin" remote.
func repoFromOrigin(repoRoot string) (owner, repo string, err error) {
	out, err := exec.Command("git", "-C", repoRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("forge: resolve GitHub repository from 'origin' remote: %w", err)
	}
	url := strings.TrimSpace(string(out))
	match := originURLPattern.FindStringSubmatch(url)
	if match == nil {
		return "", "", fmt.Errorf("forge: 'origin' remote %q is not a recognizable GitHub URL", url)
	}
	return match[1], match[2], nil
}
