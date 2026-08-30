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
	"github.com/Teagan42/forge/internal/planengine"
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

	eng := engine.New(store, trk, wsMgr, ag, cfg, repoRoot)
	// eng.Semantic (issue #126) is wired unconditionally, like
	// Publisher/PRTracker below: the SemanticProvider seam degrades to
	// fully inert on its own whenever cfg.LSP.Enabled is false (the
	// default) or ag declares no agent.SemanticProfile, so there is no
	// "not built yet" reason to leave it unset.
	eng.Semantic = semantic.NewProvider(ag, lspToSemanticConfig(cfg.LSP, cfg.Agent.Provider))
	// trk implements tracker.Tracker in full, a superset of both
	// engine.IssueFetcher (Tracker, above) and engine.NeedsInfoTracker.
	eng.NeedsInfoTracker = trk
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
		eng.Reviewer = agentreviewer.New(ag, cfg.Workflow.ReviewConfidenceFloor)
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
		// trk implements tracker.Tracker in full, a superset of engine.PRCreator.
		eng.PRTracker = trk
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
		sup := ci.New(store, trk, cfg, eng.BaseBranch)
		sup.StatusTracker = trk
		// trk implements tracker.Tracker in full, a superset of
		// ci.NeedsInfoTracker: Wait routes an unresolvable merge conflict or
		// ambiguous PR review feedback to NEEDS_INFO (issue 109) using the
		// same label/comment side effects eng.NeedsInfoTracker uses above.
		sup.NeedsInfoTracker = trk
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

// buildScheduler wires a *scheduler.Scheduler (ticket 26) for a `forge
// execute` invocation over issueIDs: the same tracker and Engine buildEngine
// would construct for a single Issue, adapted to scheduler.Executor via
// scheduler.Adapt, plus a completionResolver DependencyResolver and a
// dependencyBaseResolver BaseResolver.
//
// An Issue with no Dependencies (or only External ones) resolves to
// cfg.Git.Base's current tip, as before. An Issue with one or more
// Dependencies within issueIDs is instead based on its Dependencies'
// resulting branches (a single Dependency's branch directly, or a
// synthetic branch integrating several — see workspace.Manager.Integrate),
// so its Worker sees its prerequisites' committed changes before it starts
// (issue #108: dependency-ordered execution must also constrain what
// repository state a dependent executes against, not merely when it runs).
func buildScheduler(store storage.Store, cfg config.Config, repoRoot string, issueIDs []string) (*scheduler.Scheduler, error) {
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
		sup := ci.New(store, trk, cfg, baseBranchName(cfg.Git.Base))
		sup.StatusTracker = trk
		sup.NeedsInfoTracker = trk
		sch.CIWatcher = sup
		sch.CIRepairer = scheduler.AdaptCIRepairer(eng)
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
//   - One Managed Dependency: resolves directly to that Dependency's
//     resulting branch (workspaces.BranchName via resolver.branchFor), so
//     the dependent's Workspace is created from it — a stacked branch
//     (main -> issue/A -> issue/B), not a fresh branch off gitBase that
//     would leave A's committed work invisible to B's Worker.
//   - More than one source (multiple Managed Dependencies, or a mix of
//     Managed and External): integrates every source into one synthetic
//     branch via workspaces.Integrate, so the dependent sees the union of
//     all its Dependencies' results. A merge conflict between Dependencies
//     surfaces as a *workspace.ConflictError, deterministically naming the
//     offending branch and paths, rather than silently dropping one
//     Dependency's changes.
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
		sources = append(sources, branch)
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
	return b.workspaces.Integrate(ctx, issueID, sources)
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
// with the fake adapter and watch state flow"). Any other provider value
// invokes the real Claude Code CLI adapter (ticket 25).
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
