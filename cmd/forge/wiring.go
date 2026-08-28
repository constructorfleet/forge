package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/claude"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/scheduler"
	"github.com/Teagan42/forge/internal/storage"
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

	resolver := newCompletionResolver(issueIDs)
	base := scheduler.BaseResolverFunc(func(context.Context, string) (string, error) {
		return resolveBaseRevision(repoRoot, cfg.Git.Base)
	})

	sch := scheduler.New(trk, scheduler.Adapt(eng), resolver, base, cfg.Execution.MaxParallel)
	sch.OnComplete = resolver.onComplete
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

// completionResolver is the default scheduler.DependencyResolver `forge
// execute` uses until ticket 27 wires in a real GitHub-merge-reachability
// check. Ticket 22 (commit/PR creation) doesn't exist yet either, so there
// is no real "PR merged" signal to check — completionResolver instead
// considers a Dependency satisfied once its prerequisite Issue, PROVIDED
// that prerequisite is itself part of this `forge execute` invocation's
// requested Issue set, has locally reached a state at or beyond REVIEWING
// (Quality Gates passed) within this same run. A prerequisite outside the
// requested set (an External Issue, CONTEXT.md) is reported unsatisfied
// with a descriptive error rather than left to hang forever, since forge
// cannot check its real merge status yet either.
type completionResolver struct {
	requested map[string]bool

	mu        sync.Mutex
	completed map[string]domain.IssueState
}

func newCompletionResolver(issueIDs []string) *completionResolver {
	requested := make(map[string]bool, len(issueIDs))
	for _, id := range issueIDs {
		requested[id] = true
	}
	return &completionResolver{requested: requested, completed: map[string]domain.IssueState{}}
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

func (r *completionResolver) Satisfied(_ context.Context, issueID, dependsOnID string) (bool, error) {
	if !r.requested[dependsOnID] {
		return false, fmt.Errorf(
			"forge: issue %s depends on %s, which is outside this execution's requested issue set; "+
				"external dependency satisfaction is not yet supported", issueID, dependsOnID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.completed[dependsOnID]
	return ok && publishReadyStates[state], nil
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
	return trk, nil
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
