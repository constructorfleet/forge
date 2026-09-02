package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/agent/claude"
	"github.com/Teagan42/forge/internal/agent/codex"
	"github.com/Teagan42/forge/internal/agent/openai"
	"github.com/Teagan42/forge/internal/agent/opencode"
	"github.com/Teagan42/forge/internal/agent/pi"
	"github.com/Teagan42/forge/internal/ci"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/execution/container"
	"github.com/Teagan42/forge/internal/execution/localhost"
	"github.com/Teagan42/forge/internal/execution/remote"
	"github.com/Teagan42/forge/internal/execution/remote/httpworker"
	"github.com/Teagan42/forge/internal/gate"
	"github.com/Teagan42/forge/internal/planengine"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/replan"
	"github.com/Teagan42/forge/internal/repolock"
	"github.com/Teagan42/forge/internal/review/agentreviewer"
	"github.com/Teagan42/forge/internal/scheduler"
	"github.com/Teagan42/forge/internal/semantic"
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
	locks := repolock.New(repoRoot)

	wsMgr, err := workspace.NewManager(repoRoot,
		workspace.WithWorktreeRoot(cfg.Git.WorktreeRoot),
		workspace.WithBranchTemplate(cfg.Git.BranchTemplate),
		workspace.WithLocker(locks),
	)
	if err != nil {
		return nil, fmt.Errorf("forge: workspace manager: %w", err)
	}

	ag, err := buildAgent(cfg)
	if err != nil {
		return nil, err
	}

	backend, err := buildExecutionBackend(cfg, wsMgr, ag, store)
	if err != nil {
		return nil, err
	}

	eng := engine.New(store, trk, wsMgr, ag, cfg, repoRoot)
	eng.Backend = backend
	// eng.Semantic (issue #126) is wired unconditionally, like
	// Publisher/PRTracker below: the SemanticProvider seam degrades to
	// fully inert on its own whenever cfg.LSP.Enabled is false (the
	// default) or ag declares no agent.SemanticProfile, so there is no
	// "not built yet" reason to leave it unset.
	eng.Semantic = semantic.NewProvider(ag, lspToSemanticConfig(cfg.LSP, cfg.Agent.Provider))
	// trk implements tracker.Tracker in full, a superset of both
	// engine.IssueFetcher (Tracker, above) and engine.NeedsInfoTracker.
	eng.NeedsInfoTracker = trk
	// trk also implements engine.FollowUpTracker (CreateIssue/AddLabel) in
	// full; wired unconditionally like NeedsInfoTracker above, since
	// automatic self reporting (issue 141) only fires when an Agent
	// actually returns FollowUps.
	eng.FollowUpTracker = trk
	// eng.Diff is wired unconditionally since it is inert without a
	// Reviewer (Engine only calls it from runReview, which itself is a
	// no-op when eng.Reviewer is nil).
	eng.Diff = gitDiffProducer{}
	// eng.Reviewer (issue #158): a fresh, single-axis (bugs/breaking/
	// security) agentreviewer.Reviewer over the same Agent used for
	// implementation, gated at cfg.Workflow.ReviewConfidenceFloor, wired
	// whenever cfg.Workflow.Review is enabled (the default) — mirroring how
	// cfg.PullRequests.Enabled gates Publisher/PRTracker above. Disabled
	// (cfg.Workflow.Review false) leaves eng.Reviewer nil, so REVIEWING
	// stays the ticket-20 auto-approve resting state exactly as before this
	// ticket.
	if cfg.Workflow.Review {
		reviewer := agentreviewer.New(ag, cfg.Workflow.ReviewConfidenceFloor)
		// Rubrics (issue #162): cfg.Workflow.ReviewRubrics names, per axis,
		// an optional file already validated readable by config.Load;
		// LoadRubricOverrides reads it into Reviewer.Rubrics so that axis's
		// embedded rubric.md/quality_rubric.md/docs_rubric.md is replaced
		// with the team's own text. A blank path (the default) leaves that
		// axis's embedded rubric untouched.
		rubrics, err := agentreviewer.LoadRubricOverrides(agentreviewer.RubricOverridePaths{
			Bugs:    cfg.Workflow.ReviewRubrics.Bugs,
			Quality: cfg.Workflow.ReviewRubrics.Quality,
			Docs:    cfg.Workflow.ReviewRubrics.Docs,
		})
		if err != nil {
			return nil, fmt.Errorf("forge: %w", err)
		}
		reviewer.Rubrics = rubrics
		eng.Reviewer = reviewer
	}
	// eng.Publisher/eng.PRTracker (ticket 22) are Engine's single
	// all-or-nothing commit/PR seam (see runCommitAndPR): with both wired,
	// an approved (or, with no Reviewer, auto-approved) Issue is committed,
	// pushed, and opened as a PR; with neither, COMMITTING is a resting
	// state. A production Publisher/PRCreator exists today, so they are
	// wired whenever pull-request publication is enabled — the default.
	// Honoring cfg.PullRequests.Enabled here gives operators a real
	// off-switch (commit locally? no — the seam is all-or-nothing, so
	// disabled means the run rests at COMMITTING having only validated) and
	// lets hermetic tests exercise the full state machine without a live
	// GitHub remote.
	if cfg.PullRequests.Enabled {
		eng.Publisher = gitPublisher{locks: locks}
		// PR_CREATING is SCM-domain work (tracker.SCM's "Change Request"
		// vocabulary — see internal/tracker's doc comments), so it is
		// wired from the SCM capability, composed independently of
		// the Tracker capability above per cfg.SCM.Type — even though both
		// currently resolve to the same *github.Client implementation.
		scm, err := buildSCM(cfg, repoRoot)
		if err != nil {
			return nil, err
		}
		// scm implements tracker.SCM plus the legacy pull-request-shaped
		// methods engine.PRCreator still depends on (see buildGitHubClient).
		eng.PRTracker = scm
	}
	// trk also implements statusreflect.Tracker (AddLabel/RemoveLabel/
	// AddComment) in full; wired unconditionally like NeedsInfoTracker/
	// PRTracker above, since the ticket-24 signal itself defaults to off
	// via cfg.StatusReflection.Enabled (see statusreflect.Apply).
	eng.StatusTracker = trk
	eng.BaseBranch = baseBranchName(cfg.Git.Base)
	// eng.TargetTip/eng.Ancestry (ticket 29) are wired unconditionally,
	// like Publisher/PRTracker above: refreshing a retried Issue's base
	// forward is always safe (it only ever adds already-merged commits),
	// so there is no "not built yet" reason to leave RetryIssue on its
	// pre-ticket-29 reuse-the-recorded-base behavior.
	eng.TargetTip = engine.TargetTipResolverFunc(func(context.Context) (string, error) {
		return resolveBaseRevision(repoRoot, cfg.Git.Base)
	})
	eng.Ancestry = gitReachabilityChecker{repoRoot: repoRoot}
	// Conservative replanning (ticket 22): a Worker reporting
	// REPLAN_REQUIRED freezes its Feature, takes the Feature planning lease
	// via the same planengine.Runtime `forge plan` uses, and records the
	// trigger as a created/reopened Decision under
	// .forge/features/<id>/decisions/.
	eng.PlanningLease = planengine.New(store)
	eng.ReplanDecisions = replan.DecisionRecorder{Decisions: &fileArtifactLoader{}}
	if cfg.PullRequests.WatchCI {
		// The CI Supervisor's checks/merge-requirements seam is CI-domain
		// work, composed independently of Tracker/SCM per cfg.CI.Type — see
		// the SCM capability above and buildCI's doc comment.
		ciCap, err := buildCI(cfg, repoRoot)
		if err != nil {
			return nil, err
		}
		sup := ci.New(store, ciCap, cfg, eng.BaseBranch)
		sup.StatusTracker = trk
		// trk implements tracker.Tracker in full, a superset of
		// ci.NeedsInfoTracker: Wait routes an unresolvable merge conflict or
		// ambiguous PR review feedback to NEEDS_INFO (issue 109) using the
		// same label/comment side effects eng.NeedsInfoTracker uses above.
		sup.NeedsInfoTracker = trk
		// wsMgr (workspace.Manager) implements ci.Rebaser (its Rebase
		// method), and gitPublisher implements ci.BranchPusher: together
		// they let Wait rebase and force-push a stale pull request's
		// Workspace branch onto eng.BaseBranch (issue 233) instead of
		// polling checks evaluated against a base GitHub already considers
		// out of date.
		publisher := gitPublisher{locks: locks}
		sup.Rebaser = wsMgr
		sup.Pusher = publisher
		// gitPublisher also implements ci.BranchResetter, so Wait can put a
		// restacked dependent branch back on its last published commit when
		// the force-push of that branch fails (docs/adr/0018).
		sup.Resetter = publisher
		sup.ConflictRestorer = publisher
		sup.ConflictResolver = ci.NewWorkspaceConflictResolver(store, wsMgr, publisher, nil, gate.ExecCommandRunner{}, cfg)
		eng.CIWaiter = sup
	}
	return eng, nil
}

// lspToSemanticConfig translates the repository's `lsp` config section
// (config.LSPConfig) into semantic.Config, the shape internal/semantic
// actually consumes. internal/semantic deliberately imports nothing from
// internal/config (it sits below internal/engine and stays a leaf), so this
// translation — including flattening LSPConfig.Capabilities, which is keyed
// by backend/provider name, down to the single override that applies to
// cfg.Agent.Provider (the one backend this Engine runs) — lives here
// instead.
func lspToSemanticConfig(lsp config.LSPConfig, agentProvider string) semantic.Config {
	providers := make(map[string]semantic.ProviderPreference, len(lsp.Providers))
	for capability, pref := range lsp.Providers {
		providers[capability] = semantic.ProviderPreference(pref)
	}

	override := lsp.Capabilities[agentProvider]

	return semantic.Config{
		Enabled:   lsp.Enabled,
		Providers: providers,
		Override: semantic.CapabilityOverride{
			Definition:      override.Definition,
			References:      override.References,
			Implementations: override.Implementations,
			Hover:           override.Hover,
			DocumentSymbol:  override.DocumentSymbol,
			WorkspaceSymbol: override.WorkspaceSymbol,
			CallHierarchy:   override.CallHierarchy,
			TypeHierarchy:   override.TypeHierarchy,
		},
	}
}

func buildOperationalEngine(store storage.Store, cfg config.Config, repoRoot string) *engine.Engine {
	return engine.New(store, nil, nil, nil, cfg, repoRoot)
}

type executeRuntime struct {
	Scheduler               *scheduler.Scheduler
	LostExecutionController *engine.LostExecutionController
}

// buildExecuteRuntime wires the long-running components that a `forge
// execute` invocation owns. The Scheduler drives requested Issues. The lost
// Execution controller runs only for the remote backend, where a Worker has
// a lease that can lapse.
//
// An Issue with no Dependencies (or only External ones) resolves to
// cfg.Git.Base's current tip, as before. An Issue with one or more
// Dependencies within issueIDs is instead based on its Dependencies'
// resulting branches (a single Dependency's branch directly, or a
// synthetic branch integrating several — see workspace.Manager.Integrate),
// so its Worker sees its prerequisites' committed changes before it starts
// (issue #108: dependency-ordered execution must also constrain what
// repository state a dependent executes against, not merely when it runs).
func buildExecuteRuntime(store storage.Store, cfg config.Config, repoRoot string, issueIDs []string) (*executeRuntime, error) {
	trk, err := buildTracker(cfg, repoRoot)
	if err != nil {
		return nil, err
	}

	eng, err := buildEngine(store, cfg, repoRoot)
	if err != nil {
		return nil, err
	}

	wsMgr, err := workspace.NewManager(repoRoot,
		workspace.WithWorktreeRoot(cfg.Git.WorktreeRoot),
		workspace.WithBranchTemplate(cfg.Git.BranchTemplate),
		workspace.WithLocker(repolock.New(repoRoot)),
	)
	if err != nil {
		return nil, fmt.Errorf("forge: workspace manager: %w", err)
	}

	resolver := newCompletionResolver(issueIDs, trk, cfg.Git.Base)
	base := &dependencyBaseResolver{
		tracker:    trk,
		resolver:   resolver,
		workspaces: wsMgr,
		repoRoot:   repoRoot,
		gitBase:    cfg.Git.Base,
	}

	sch := scheduler.New(trk, scheduler.Adapt(eng), resolver, base, cfg.Execution.MaxParallel)
	sch.OnComplete = resolver.onComplete
	if cfg.PullRequests.WatchCI {
		// See buildEngine's identical wiring for why the CI capability is
		// built independently of trk (the Tracker capability) here.
		ciCap, err := buildCI(cfg, repoRoot)
		if err != nil {
			return nil, err
		}
		sup := ci.New(store, ciCap, cfg, baseBranchName(cfg.Git.Base))
		sup.StatusTracker = trk
		sup.NeedsInfoTracker = trk
		// See buildEngine's identical wiring for why wsMgr/gitPublisher
		// satisfy ci.Rebaser/ci.BranchPusher (issue 233).
		publisher := gitPublisher{locks: repolock.New(repoRoot)}
		sup.Rebaser = wsMgr
		sup.Pusher = publisher
		// See buildEngine's identical wiring for why gitPublisher also
		// satisfies ci.BranchResetter (docs/adr/0018).
		sup.Resetter = publisher
		sup.ConflictRestorer = publisher
		sup.ConflictResolver = ci.NewWorkspaceConflictResolver(store, wsMgr, publisher, nil, gate.ExecCommandRunner{}, cfg)
		sch.CIWatcher = sup
		sch.CIRepairer = scheduler.AdaptCIRepairer(eng)
	}
	runtime := &executeRuntime{Scheduler: sch}
	if lostRecoveryEnabled(cfg) {
		runtime.LostExecutionController = engine.NewLostExecutionController(store, store, eng, time.Now)
	}
	return runtime, nil
}

// buildScheduler wires a *scheduler.Scheduler (ticket 26) for a `forge
// execute` invocation over issueIDs.
func buildScheduler(store storage.Store, cfg config.Config, repoRoot string, issueIDs []string) (*scheduler.Scheduler, error) {
	runtime, err := buildExecuteRuntime(store, cfg, repoRoot, issueIDs)
	if err != nil {
		return nil, err
	}
	return runtime.Scheduler, nil
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

	mu sync.Mutex
	// completed records each Managed prerequisite's terminal state and the
	// Execution it ran under, once it reaches or beyond REVIEWING — the
	// latter is what dependencyBaseResolver.CurrentBase needs to resolve
	// that prerequisite's resulting branch via
	// workspace.Manager.BranchName(executionID, issueID).
	completed map[string]completionInfo
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

// completionInfo is what completionResolver records for each Managed
// prerequisite once its Executor.Execute call returns.
type completionInfo struct {
	state       domain.IssueState
	executionID string
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
		completed:  map[string]completionInfo{},
		external:   map[string]tracker.ExternalState{},
	}
}

// onComplete is wired as the Scheduler's OnComplete hook: it records each
// dispatched Issue's final state and the Execution it ran under as it
// finishes, which Satisfied and branchFor then consult.
func (r *completionResolver) onComplete(issueID, executionID string, state domain.IssueState, err error) {
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed[issueID] = completionInfo{state: state, executionID: executionID}
}

func (r *completionResolver) Satisfied(ctx context.Context, _, dependsOnID string) (bool, error) {
	if !r.requested[dependsOnID] {
		return r.externalSatisfied(ctx, dependsOnID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.completed[dependsOnID]
	return ok && publishReadyStates[info.state], nil
}

// branchFor resolves the resulting branch of a Managed prerequisite
// (issueID) that has already been recorded satisfied by onComplete, via
// workspaces.BranchName(executionID, issueID). ok is false if issueID has
// not (yet) completed to a satisfied state — dependencyBaseResolver only
// calls this once Scheduler has already confirmed Satisfied, so that
// should never happen in practice, but branchFor reports it explicitly
// rather than returning a nonsensical branch name.
func (r *completionResolver) branchFor(issueID string, workspaces *workspace.Manager) (string, bool) {
	r.mu.Lock()
	info, ok := r.completed[issueID]
	r.mu.Unlock()
	if !ok || !publishReadyStates[info.state] {
		return "", false
	}
	return workspaces.BranchName(info.executionID, issueID), true
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

// dependencyBaseResolver is the scheduler.BaseResolver `forge execute`
// uses (issue #108): unlike a fixed-base resolver, it accounts for each
// Issue's Dependencies within the requested execution set when resolving
// what base its Workspace should be built on.
//
//   - No Dependencies (or only External ones, already required to be
//     merged-and-reachable from gitBase before they satisfy — see
//     completionResolver.externalSatisfied): resolves to gitBase's current
//     tip, exactly as an Issue with no Dependencies always has.
//   - One Managed Dependency: resolves that Dependency's resulting branch
//     (workspaces.BranchName via resolver.branchFor) to its current commit
//     SHA, so the dependent's Workspace is created from a pinned commit —
//     a stacked branch (main -> issue/A -> issue/B), not a fresh branch off
//     gitBase that would leave A's committed work invisible to B's Worker.
//     Pinning to a SHA, not the moving branch name, keeps this base valid
//     after A's branch merges and disappears (docs/adr/0018, ticket #330).
//   - More than one source (multiple Managed Dependencies, or a mix of
//     Managed and External): integrates every source into one synthetic
//     branch via workspaces.Integrate, then resolves that integration
//     branch to its current commit SHA, so the dependent sees the union of
//     all its Dependencies' results pinned at a fixed point. A merge
//     conflict between Dependencies surfaces as a *workspace.ConflictError,
//     deterministically naming the offending branch and paths, rather than
//     silently dropping one Dependency's changes.
//
// CurrentBase is only ever called for an Issue whose Dependencies
// resolver.Resolver (DependencyResolver) has already reported Satisfied,
// per Scheduler.Run's dispatch order — so every Managed Dependency here is
// expected to already have a recorded branchFor entry.
type dependencyBaseResolver struct {
	tracker    scheduler.IssueFetcher
	resolver   *completionResolver
	workspaces *workspace.Manager
	repoRoot   string
	gitBase    string
}

func (b *dependencyBaseResolver) CurrentBase(ctx context.Context, issueID string) (string, error) {
	issue, err := b.tracker.GetIssue(ctx, issueID)
	if err != nil {
		return "", fmt.Errorf("forge: resolve base for issue %s: fetch issue: %w", issueID, err)
	}
	if len(issue.Dependencies) == 0 {
		return resolveBaseRevision(b.repoRoot, b.gitBase)
	}

	var sources []string
	hasExternal := false
	for _, dep := range issue.Dependencies {
		if !b.resolver.requested[dep.DependsOnID] {
			hasExternal = true
			continue
		}
		branch, ok := b.resolver.branchFor(dep.DependsOnID, b.workspaces)
		if !ok {
			return "", fmt.Errorf(
				"forge: resolve base for issue %s: dependency %s has not completed", issueID, dep.DependsOnID)
		}
		sha, err := resolveBaseRevision(b.repoRoot, branch)
		if err != nil {
			return "", fmt.Errorf(
				"forge: resolve base for issue %s: pin dependency %s's branch to a SHA: %w", issueID, dep.DependsOnID, err)
		}
		sources = append(sources, sha)
	}

	if len(sources) == 0 {
		// Every Dependency is External; those are only ever Satisfied once
		// merged and reachable from gitBase (completionResolver.externalSatisfied),
		// so gitBase's current tip already contains their results.
		return resolveBaseRevision(b.repoRoot, b.gitBase)
	}

	if hasExternal {
		// A mix of Managed and External Dependencies: fold gitBase's
		// current tip in alongside the Managed branches so an External
		// Dependency's merged code is included too, in case it landed
		// after the Managed branch(es) were created.
		baseTip, err := resolveBaseRevision(b.repoRoot, b.gitBase)
		if err != nil {
			return "", err
		}
		sources = append(sources, baseTip)
	}

	if len(sources) == 1 {
		return sources[0], nil
	}
	integrated, err := b.workspaces.Integrate(ctx, issueID, sources)
	if err != nil {
		return "", err
	}
	return resolveBaseRevision(b.repoRoot, integrated)
}

// buildTracker constructs the Tracker capability for repoRoot, resolving
// the target repository from its "origin" remote and selecting an
// implementation by cfg.Tracker.Type. Factored out of buildEngine so
// `forge resume` (which needs the full tracker.Tracker for GetComments, not
// just engine.IssueFetcher) can build the same adapter without constructing
// a whole Engine. Only "github" is implemented today; cfg.Tracker.Type is
// otherwise rejected by config.Load's validation before wiring ever runs
// (see config.validate), so the default case here is unreachable in
// practice and exists only as a defensive backstop, matching buildAgent's
// provider switch.
func buildTracker(cfg config.Config, repoRoot string) (*github.Client, error) {
	return resolveCapability("tracker", cfg.Tracker.Type, cfg, repoRoot)
}

// buildSCM constructs the SCM capability for repoRoot, composed
// independently of buildTracker per cfg.SCM.Type (see config.Config.
// Provider's doc comment on capability composition). Only "github" is
// implemented today, mirroring buildTracker's defensive default case.
func buildSCM(cfg config.Config, repoRoot string) (*github.Client, error) {
	return resolveCapability("scm", cfg.SCM.Type, cfg, repoRoot)
}

// buildCI constructs the CI capability for repoRoot, composed independently
// of buildTracker/buildSCM per cfg.CI.Type. config.Load's frozen
// composition rule (config.validate) already guarantees cfg.CI.Type equals
// cfg.SCM.Type (or names a recognized external-status observer) by the
// time wiring runs, so this switch only needs to know how to construct
// each recognized type, not re-check coherence. Only "github" is
// implemented today.
func buildCI(cfg config.Config, repoRoot string) (*github.Client, error) {
	return resolveCapability("ci", cfg.CI.Type, cfg, repoRoot)
}

// resolveCapability constructs the client for one capability (tracker/scm/
// ci), sharing buildTracker/buildSCM/buildCI's identical
// "github" -> buildGitHubClient, else error resolution — only the field
// read and the capability label differ between them.
func resolveCapability(capability, providerType string, cfg config.Config, repoRoot string) (*github.Client, error) {
	switch providerType {
	case "github":
		return buildGitHubClient(cfg, repoRoot)
	default:
		return nil, fmt.Errorf("forge: unknown %s provider type %q", capability, providerType)
	}
}

// buildGitHubClient constructs the github.Client shared by buildTracker,
// buildSCM, and buildCI's "github" cases: github.Client satisfies all three
// capability interfaces (tracker.Tracker, tracker.SCM, tracker.CI) plus the
// legacy pull-request/check-shaped methods Engine and the CI Supervisor's
// existing narrow consumer seams still depend on, so a composition that
// resolves every capability to "github" wires the same wiring these seams
// already exercised before the capability split.
func buildGitHubClient(cfg config.Config, repoRoot string) (*github.Client, error) {
	owner, repo, err := repoFromOrigin(repoRoot)
	if err != nil {
		return nil, err
	}
	trk := github.NewClient(nil, "", owner, repo)
	trk.Provider = cfg.Tracker.Provider
	trk.DependencyOverrides = cfg.Dependencies.Overrides
	// Reachability is only exercised by CheckExternal (ticket 27); wiring
	// it unconditionally here, rather than only for `forge execute`, keeps
	// this one Client construction path fully usable by any future caller
	// that needs external-dependency satisfaction (e.g. an eventual
	// `forge resume` extension) without threading it through separately.
	trk.Reachability = gitReachabilityChecker{repoRoot: repoRoot}
	return trk, nil
}

// verifyTrackerAuth runs the configured tracker's authentication preflight,
// if it has one, before `forge execute`/`forge resume` do any
// side-effecting work (opening the state store, creating a workspace,
// invoking an agent, or transitioning an Issue). Two escape hatches exist
// for contexts that legitimately need no tracker credential: a Tracker
// implementation that needs no credential (e.g. a fake/offline tracker)
// simply doesn't implement tracker.AuthPreflighter, so the type assertion
// below fails and this is a no-op; alternatively cfg.Tracker.SkipAuthPreflight
// opts out without even constructing a tracker.
func verifyTrackerAuth(ctx context.Context, cfg config.Config, repoRoot string) error {
	if cfg.Tracker.SkipAuthPreflight {
		return nil
	}
	trk, err := buildTracker(cfg, repoRoot)
	if err != nil {
		return err
	}
	preflighter, ok := interface{}(trk).(tracker.AuthPreflighter)
	if !ok {
		return nil
	}
	if err := preflighter.VerifyAuth(ctx); err != nil {
		return fmt.Errorf("forge: tracker authentication preflight failed: %w", err)
	}
	return nil
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
// with the fake adapter and watch state flow"). Empty defaults to the real
// Claude Code CLI adapter (ticket 25).
//
// The same Agent this returns is also wrapped by agentreviewer.Reviewer
// (issue #158, see buildEngine's eng.Reviewer wiring) whenever
// cfg.Workflow.Review is enabled, so the "fake" case's default Summary must
// double as a review findings envelope agentreviewer can parse (a clean,
// empty-findings one) rather than only reading naturally as an
// implementation summary.
func buildAgent(cfg config.Config) (agent.Agent, error) {
	switch cfg.Agent.Provider {
	case "fake":
		fake := agent.NewFakeAgent()
		fake.ProgramDefault(agent.AgentResult{
			Status:  agent.StatusImplemented,
			Summary: `fake agent: no-op implementation {"axis":"bugs","findings":[]}`,
		})
		return fake, nil
	case "claude-code", "":
		return &claude.Adapter{
			PermissionMode: string(cfg.Agent.PermissionMode),
			Timeout:        cfg.Agent.Timeout,
		}, nil
	case "codex":
		return &codex.Adapter{}, nil
	case "opencode":
		return &opencode.Adapter{}, nil
	case "pi":
		return &pi.Adapter{}, nil
	case "openai-responses":
		return &openai.ResponsesAdapter{}, nil
	case "openai-chat-completions":
		return &openai.ChatCompletionsAdapter{}, nil
	default:
		return nil, fmt.Errorf("forge: unknown agent provider %q", cfg.Agent.Provider)
	}
}

// buildExecutionBackend selects the execution.ExecutionBackend Engine
// prepares each Worker's environment from, per cfg.Execution.Backend (issue
// #304, constructorfleet/forge#285: execution-location configuration). Empty
// and config.BackendLocal both select localhost.Backend, the in-process
// backend built from wsMgr/ag — the same WorkspaceCreator and Agent
// buildEngine wires everywhere else, so a Worker's environment and Engine's
// own Workspaces/Agent fields always agree on which Workspace and Agent they
// mean. config.BackendContainer selects the Container backend (issue #336);
// buildContainerRuntime's preflight failure surfaces here, so an
// unavailable container runtime is a wiring-time error, not a mid-run one.
// config.BackendRemote selects the Remote backend (issue #343), targeting
// the single statically-configured worker named by cfg.Execution.Worker;
// buildWorkerClient's preflight failure surfaces here the same way. store
// backs the Remote backend's loss recovery (issue #344): a WorkerClient
// error is only ever routed to LOST after engine.RecoverLostExecution
// confirms the ExecutionLease has lapsed, never merely because Execute or
// RunAgent returned an error. config.Load's validation already rejects any
// other value before wiring ever runs (see config.validate); the default
// case here is a defensive backstop, matching buildAgent's provider switch.
func buildExecutionBackend(cfg config.Config, wsMgr *workspace.Manager, ag agent.Agent, store storage.Store) (execution.ExecutionBackend, error) {
	switch cfg.Execution.Backend {
	case config.BackendLocal, "":
		return localhost.NewBackend(wsMgr, ag), nil
	case config.BackendContainer:
		runtime, err := buildContainerRuntime(cfg)
		if err != nil {
			return nil, fmt.Errorf("forge: container runtime preflight: %w", err)
		}
		resources := container.Resources{CPU: cfg.Execution.Container.CPU, Memory: cfg.Execution.Container.Memory}
		return container.NewBackend(wsMgr, runtime, cfg.Execution.Container.Image, resources, nil), nil
	case config.BackendRemote:
		worker, err := buildWorkerClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("forge: remote worker preflight: %w", err)
		}
		return remote.NewBackendWithLeases(worker, buildRemoteRecoverFunc(store), store), nil
	default:
		return nil, fmt.Errorf("forge: unknown execution backend %q", cfg.Execution.Backend)
	}
}

// lostRecoveryPollInterval is how often `forge execute` checks in-flight
// remote executions for a lapsed heartbeat. It mirrors
// scheduler.defaultPollInterval's role as a periodic fallback. It is
// independent of any single WorkerClient call, so Forge can detect a lost
// Worker when no request is in flight.
const lostRecoveryPollInterval = 10 * time.Second

// lostRecoveryEnabled reports whether `forge execute` should run the
// background loss detection for this Execution. An ExecutionLease is only
// ever claimed under the Remote ExecutionBackend (issue #343), so the loop
// has no effect under every other backend and is skipped entirely.
func lostRecoveryEnabled(cfg config.Config) bool {
	return cfg.Execution.Backend == config.BackendRemote
}

// reportLostExecutionControllerError writes controller reconciliation
// failures beside the other `forge execute` diagnostics. The controller
// keeps running after a failed pass.
func reportLostExecutionControllerError(err error) {
	fmt.Fprintf(os.Stderr, "forge execute: lost-execution recovery: %v\n", err)
}

// buildRemoteRecoverFunc adapts engine.RecoverLostExecution to
// remote.RecoverFunc, so the Remote backend can tell a vanished worker
// (heartbeat lapse) from a worker-reported failure without the Engine
// itself knowing which backend is running (issue #344).
func buildRemoteRecoverFunc(store storage.Store) remote.RecoverFunc {
	return func(ctx context.Context, executionID, issueID string) (bool, error) {
		result, err := engine.RecoverLostExecution(ctx, store, executionID, issueID, time.Now)
		if err != nil {
			var exhausted *domain.RetryExhaustedError
			if errors.As(err, &exhausted) {
				return true, nil
			}
			return false, err
		}
		return result.Lost, nil
	}
}

// containerPreflightTimeout bounds how long buildContainerRuntime waits for
// a candidate CLI binary's daemon to answer, so an unreachable docker/podman
// daemon fails preflight quickly rather than hanging wiring.
const containerPreflightTimeout = 5 * time.Second

// buildContainerRuntime constructs the container.ContainerRuntime the
// Container backend drives: container.DetectCLIRuntime probes docker, then
// podman, and picks the first whose daemon actually answers, through
// container.ExecCommandRunner (issue #385). A daemon that never answers
// within containerPreflightTimeout, or no CLI binary that answers at all,
// fails this preflight with container.ErrRuntimeUnavailable, rather than
// reaching Prepare and failing mid-run against a nil runtime.
func buildContainerRuntime(_ config.Config) (container.ContainerRuntime, error) {
	ctx, cancel := context.WithTimeout(context.Background(), containerPreflightTimeout)
	defer cancel()
	return container.DetectCLIRuntime(ctx, container.ExecCommandRunner{})
}

// workerPreflightTimeout bounds how long buildWorkerClient waits for the
// configured worker to answer its health check, so an unreachable worker
// fails preflight quickly rather than hanging wiring.
const workerPreflightTimeout = 5 * time.Second

// buildWorkerClient constructs the remote.WorkerClient the Remote backend
// drives, against the single statically-configured worker named by
// cfg.Execution.Worker.Endpoint. It is httpworker.Client (issue #345), the
// one concrete WorkerClient transport: plain HTTP+JSON against a worker
// daemon (httpworker.Server) running behind that endpoint. Ping confirms
// the worker answers its health check before any work is dispatched;
// a failure there is wrapped in remote.ErrWorkerUnreachable, matching the
// sentinel this preflight always returned before a concrete transport
// existed.
func buildWorkerClient(cfg config.Config) (remote.WorkerClient, error) {
	client := httpworker.NewClient(cfg.Execution.Worker.Endpoint, &http.Client{Timeout: workerPreflightTimeout})
	ctx, cancel := context.WithTimeout(context.Background(), workerPreflightTimeout)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", remote.ErrWorkerUnreachable, cfg.Execution.Worker.Endpoint, err)
	}
	return client, nil
}

// buildPlanningBackend selects the planning Backend `forge plan` runs
// against, per cfg.Agent.Provider -- reusing buildAgent's provider switch.
// "fake" returns the scripted planningagent.FakeBackend used by tests and
// demos; every production provider wraps buildAgent's agent.Agent in
// planningagent.AgentBackend so planning genuinely invokes the configured
// coding backend. Any other provider value is an error, matching buildAgent.
//
// The real backend is built with store so every planning invocation --
// wayfinding, spec generation/review, ticket-plan generation/review, all of
// which reach the Agent through this single Backend -- records an
// agent_runs row and streams its transcript into transcript_events (issue
// #248), the way execution and review agents already do.
//
// featureID is used as both the execution_id and the issue_id those rows are
// keyed by. `forge plan` has no single durable execution id spanning its
// stages (runWayfindingStage opens a planning_executions row, but only when
// wayfinding actually runs, and spec/ticket-plan generation never sees one),
// and planning has no repeating "issue" concept the way ticket execution
// does -- so the Feature is the one stable scope every stage shares, and
// correlating a Feature's whole planning transcript is a single lookup.
// FakeBackend records nothing: it never reaches an agent.Agent at all.
func buildPlanningBackend(cfg config.Config, store planningagent.TranscriptStore, featureID string) (planningagent.Backend, error) {
	switch cfg.Agent.Provider {
	case "fake":
		return planningagent.NewFakeBackend(), nil
	default:
		ag, err := buildAgent(cfg)
		if err != nil {
			return nil, err
		}
		return planningagent.NewPersistingAgentBackend(ag, store, featureID, featureID), nil
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
