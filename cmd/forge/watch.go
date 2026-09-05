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
)

const watchUsage = `Usage: forge watch [execution-id]

Attach the live roster to one Execution. Without an id, attaches only when
exactly one Execution is currently active; otherwise it lists the candidates
and exits 2. The id is resolved by probing the executions and
planning_executions store tables (never the filesystem).

  --db      path to the SQLite state database (default .forge/forge.db)
  --config  path to .forge.yaml (passed to the operational Engine constructor; unused by the current cancel action)
`

// watchProber is the read-only store surface watch needs to disambiguate an
// id across both ID spaces.
type watchProber interface {
	LoadExecution(ctx context.Context, id string) (storage.ExecutionState, error)
	LoadPlanningExecution(ctx context.Context, id string) (domain.PlanningExecution, error)
}

// watchTarget records which ID space an id resolved into.
type watchTarget struct {
	id       string
	isCoding bool
}

// resolveWatchTarget probes the executions table first, then
// planning_executions, resolving id to whichever space holds it (user story
// 36: explicit store probe, never the filesystem). It returns an error when
// id is in neither space.
func resolveWatchTarget(ctx context.Context, store watchProber, id string) (watchTarget, error) {
	if _, err := store.LoadExecution(ctx, id); err == nil {
		return watchTarget{id: id, isCoding: true}, nil
	}
	if _, err := store.LoadPlanningExecution(ctx, id); err == nil {
		return watchTarget{id: id, isCoding: false}, nil
	}
	return watchTarget{}, fmt.Errorf("no execution or planning execution with id %q", id)
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

// listLiveExecutions returns the currently-active coding Executions (those
// with at least one not-yet-terminal Issue).
func listLiveExecutions(ctx context.Context, store *storage.SQLiteStore) ([]liveExecutionSummary, error) {
	summaries, err := engine.ListActiveExecutions(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("forge: list executions: %w", err)
	}
	live := make([]liveExecutionSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.ActiveIssues <= 0 {
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

func runWatch(args []string) int {
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

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}

	// Watch is a read-only observer: never run Migrate, and fail loudly
	// against a schema that predates the live roster rather than misrender.
	store, err := storage.Open(*dbPath)
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
		live, err := listLiveExecutions(ctx, store)
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
			return 1
		}
		switch len(live) {
		case 0:
			fmt.Fprintln(os.Stderr, "forge watch: no active executions")
			return 1
		case 1:
			target = watchTarget{id: live[0].ID, isCoding: true}
		default:
			fmt.Fprintf(os.Stderr, "forge watch: %d active executions; pass one:\n", len(live))
			for _, e := range live {
				fmt.Fprintf(os.Stderr, "  %s  base=%s  started=%s  active=%d done=%d failed=%d\n",
					e.ID, e.Base, e.Started.Format(time.RFC3339), e.Active, e.Done, e.Failed)
			}
			return 2
		}
	case 1:
		target, err = resolveWatchTarget(ctx, store, fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
			return 1
		}
	}

	answerer := resolveAnswerer(ctx, cfg, repoRoot)
	if !target.isCoding {
		if err := runPlanningRoster(ctx, store, target.id, answerer); err != nil {
			fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
			return 1
		}
		return 0
	}

	// One operational Engine instance wires both the cancel and approve
	// controls: it satisfies both narrow seams. The retry control uses a
	// separate detached forge child.
	operationalEngine := buildOperationalEngine(store, cfg, repoRoot)
	retrier := resolveRetrier(*configPath, *dbPath)
	if err := runLiveRoster(ctx, store, target.id, operationalEngine, retrier, operationalEngine, answerer); err != nil {
		fmt.Fprintf(os.Stderr, "forge watch: %v\n", err)
		return 1
	}
	return 0
}
