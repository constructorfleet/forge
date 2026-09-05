package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

const watchUsage = `Usage: forge watch [execution-id]

Attach the live roster to one Execution. Without an id, attaches only when
exactly one Execution has a live Worker heartbeat; otherwise it lists the
candidates and exits 2. The id is resolved by probing, in order, the
executions table, the planning_executions table, and Feature ids (via
agent_runs). It never probes the filesystem.

  --db      path to the SQLite state database (default .forge/forge.db)
  --config  path to .forge.yaml (passed to the operational Engine constructor; unused by the current cancel action)
`

// watchProber is the read-only store surface watch needs to disambiguate an
// id across all three ID spaces.
type watchProber interface {
	LoadExecution(ctx context.Context, id string) (storage.ExecutionState, error)
	LoadPlanningExecution(ctx context.Context, id string) (domain.PlanningExecution, error)
	FeatureHasPlanningRuns(ctx context.Context, featureID string) (bool, error)
}

// watchTargetKind records which ID space an id resolved into.
type watchTargetKind int

const (
	watchTargetCoding watchTargetKind = iota
	watchTargetPlanningExecution
	watchTargetFeature
)

// watchTarget records which ID space an id resolved into.
type watchTarget struct {
	id   string
	kind watchTargetKind
}

// resolveWatchTarget probes the executions table, then planning_executions,
// then Feature via a planning-backend agent_runs row. These are explicit
// store probes, never the filesystem. A 36-char UUID is a legal Feature id,
// so an os.Stat could collide with it. resolveWatchTarget fails loudly,
// rather than picking the first match, when id resolves in more than one
// space.
func resolveWatchTarget(ctx context.Context, store watchProber, id string) (watchTarget, error) {
	var matches []watchTarget
	if _, err := store.LoadExecution(ctx, id); err == nil {
		matches = append(matches, watchTarget{id: id, kind: watchTargetCoding})
	}
	if _, err := store.LoadPlanningExecution(ctx, id); err == nil {
		matches = append(matches, watchTarget{id: id, kind: watchTargetPlanningExecution})
	}
	ok, err := store.FeatureHasPlanningRuns(ctx, id)
	if err != nil {
		return watchTarget{}, fmt.Errorf("probe feature %q: %w", id, err)
	}
	if ok {
		matches = append(matches, watchTarget{id: id, kind: watchTargetFeature})
	}
	switch len(matches) {
	case 0:
		return watchTarget{}, fmt.Errorf("no execution, planning execution, or feature with id %q", id)
	case 1:
		return matches[0], nil
	default:
		return watchTarget{}, fmt.Errorf("id %q is ambiguous: it matches more than one of executions, planning_executions, and agent_runs (feature)", id)
	}
}

// liveExecutionSummary pairs an Execution with its active-issue count, the
// shape bare-watch needs to decide attach-vs-list.
type liveExecutionSummary struct {
	ID      string
	Base    string
	Started time.Time
	Active  int
	Done    int
	Failed  int
}

// listLiveExecutions returns the coding Executions that currently have a
// live (non-stale) Worker heartbeat as of now. This is the issue #485
// definition of "live" that bare `forge watch` disambiguates against. It is
// distinct from merely having a not-yet-terminal Issue: a wedged or exited
// Worker still leaves its Issue non-terminal, with no one left beating its
// claim. It uses tui.StaleHeartbeat, the same threshold the roster uses to
// render a row as Stale, so the two views agree on what counts as live.
func listLiveExecutions(ctx context.Context, store *storage.SQLiteStore, now time.Time) ([]liveExecutionSummary, error) {
	summaries, err := engine.ListActiveExecutions(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("forge: list executions: %w", err)
	}
	liveIDs, err := store.LiveWorkerExecutionIDs(ctx, now.Add(-tui.StaleHeartbeat))
	if err != nil {
		return nil, fmt.Errorf("forge: list live worker executions: %w", err)
	}
	liveSet := make(map[string]bool, len(liveIDs))
	for _, id := range liveIDs {
		liveSet[id] = true
	}

	live := make([]liveExecutionSummary, 0, len(summaries))
	for _, s := range summaries {
		if !liveSet[s.Execution.ID] {
			continue
		}
		live = append(live, liveExecutionSummary{
			ID:      s.Execution.ID,
			Base:    s.Execution.BaseRevision,
			Started: s.Execution.StartedAt,
			Active:  s.ActiveIssues,
			Done:    s.DoneIssues,
			Failed:  s.FailedIssues,
		})
	}
	return live, nil
}

// runWatch is the `forge watch` command boundary. The first recover() sits
// here, not in main. A call to os.Exit skips main's deferred functions. This
// way a panic anywhere in the read path becomes a clean exit code 1, not a
// crash.
func runWatch(args []string) int {
	return withPanicGuard("forge watch", func() int { return doRunWatch(args) })
}

func doRunWatch(args []string) int {
	fs := flag.NewFlagSet("forge watch", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml (passed to the operational Engine constructor; unused by the current cancel action)")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "forge watch: expected at most one argument, [execution-id]")
		return 2
	}

	ctx := context.Background()

	repoRoot, err := discoverRepoRootOrCWD()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}
	resolvedConfigPath, resolvedDBPath := resolveConfigDBPaths(fs, repoRoot, *configPath, *dbPath)
	cfg, err := loadConfig(resolvedConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}

	// Watch is a read-only observer: never run Migrate, and fail loudly
	// against a schema that predates the live roster rather than misrender.
	store, err := storage.Open(resolvedDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	ok, err := store.LivenessColumnsPresent(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "forge watch: this database predates the live roster; run 'forge execute' once to migrate it\n")
		return 1
	}

	var target watchTarget
	switch fs.NArg() {
	case 0:
		live, err := listLiveExecutions(ctx, store, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
			return 1
		}
		if len(live) != 1 {
			if len(live) == 0 {
				fmt.Fprintln(os.Stderr, "forge watch: no execution has a live worker heartbeat")
			} else {
				fmt.Fprintf(os.Stderr, "forge watch: %d executions have a live worker heartbeat; pass one:\n", len(live))
				for _, e := range live {
					fmt.Fprintf(os.Stderr, "  %s  base=%s  started=%s  active=%d done=%d failed=%d\n",
						e.ID, e.Base, e.Started.Format(time.RFC3339), e.Active, e.Done, e.Failed)
				}
			}
			return 2
		}
		target = watchTarget{id: live[0].ID, kind: watchTargetCoding}
	case 1:
		target, err = resolveWatchTarget(ctx, store, fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
			return 1
		}
	}

	answerer := resolveAnswerer(ctx, cfg, repoRoot)
	var runPlanning func() error
	switch target.kind {
	case watchTargetPlanningExecution:
		runPlanning = func() error { return runPlanningRoster(ctx, store, target.id, answerer, repoRoot) }
	case watchTargetFeature:
		runPlanning = func() error { return runPlanningRosterForFeature(ctx, store, target.id, answerer, repoRoot) }
	}
	if runPlanning != nil {
		if err := runPlanning(); err != nil {
			fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
			return 1
		}
		return 0
	}

	// One operational Engine instance wires both the cancel and approve
	// controls: it satisfies both narrow seams. The retry control uses a
	// separate detached forge child.
	operationalEngine := buildOperationalEngine(store, cfg, repoRoot)
	retrier := resolveRetrier(resolvedConfigPath, resolvedDBPath)
	if err := runLiveRoster(ctx, store, target.id, operationalEngine, retrier, operationalEngine, answerer); err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}
	return 0
}
