package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

// runExecute implements `forge execute <issue-number> [<issue-number> ...]`
// (ticket 26 extends ticket 18's single-issue vertical slice to multiple
// Issues). It resolves the Dependency DAG across every requested Issue,
// then drives each through Forge's state machine via internal/scheduler,
// running independent Issues concurrently up to execution.max_parallel and
// holding dependency-blocked Issues until their prerequisites are
// satisfied (see cmd/forge's completionResolver). It prints each Issue's
// final state and exits non-zero if any Issue errored.
func runExecute(args []string) int {
	fs := flag.NewFlagSet("forge execute", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path to .forge.yaml")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite state database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "forge execute: expected at least one argument, <issue-number> [<issue-number> ...]")
		return 2
	}
	issueIDs := fs.Args()

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}
	if msg, stale := staleBinaryWarning(repoRoot, buildCommit); stale {
		fmt.Fprintln(os.Stderr, msg)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := verifyTrackerAuth(ctx, cfg, repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	runtime, err := buildExecuteRuntime(store, cfg, repoRoot, issueIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	if runtime.LostExecutionController != nil {
		stopLostRecovery := startBackgroundController(ctx, runtime.LostExecutionController, lostRecoveryPollInterval, reportLostExecutionControllerError)
		defer stopLostRecovery()
	}

	if runtime.ProviderLimitController != nil {
		stopProviderLimit := startBackgroundController(ctx, runtime.ProviderLimitController, providerLimitPollInterval, reportProviderLimitControllerError)
		defer stopProviderLimit()
	}

	results, err := runtime.Scheduler.Run(ctx, issueIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forge execute: %v\n", err)
		return 1
	}

	exitCode := 0
	for _, id := range issueIDs {
		res := results[id]
		if res.Err != nil {
			fmt.Printf("issue %s -> error: %v\n", id, res.Err)
			exitCode = 1
			continue
		}
		fmt.Printf("issue %s -> %s\n", id, res.State)
	}
	return exitCode
}

// backgroundControllerRunner is the shape every reconciliation loop `forge
// execute` runs in the background shares: engine.LostExecutionController and
// engine.ProviderLimitController both satisfy it.
type backgroundControllerRunner interface {
	Run(ctx context.Context, interval time.Duration, onErr func(error)) error
}

// startBackgroundController runs controller in its own goroutine on the given
// poll interval. It returns a stop function that cancels the loop and waits
// for it to finish, so `forge execute` never returns while a loop still runs.
func startBackgroundController(ctx context.Context, controller backgroundControllerRunner, interval time.Duration, onErr func(error)) func() {
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := controller.Run(loopCtx, interval, onErr); err != nil && !errors.Is(err, context.Canceled) && onErr != nil {
			onErr(err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
