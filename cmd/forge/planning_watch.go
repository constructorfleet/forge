package main

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/Teagan42/forge/internal/planningapprove"
	"github.com/Teagan42/forge/internal/repolock"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tui"
)

// buildPlanningModel resolves planningExecutionID to its Feature and wires a
// PlanningModel over store, the planning-phase analogue of runLiveRoster's
// wiring: a live *planningapprove.Approver backs the approve key (issue
// #606), and answerer (from resolveAnswerer, nil-able) backs the answer key.
func buildPlanningModel(ctx context.Context, store *storage.SQLiteStore, planningExecutionID string, answerer tui.Answerer, repoRoot string) (*tui.PlanningModel, error) {
	pe, err := store.LoadPlanningExecution(ctx, planningExecutionID)
	if err != nil {
		return nil, fmt.Errorf("forge watch: load planning execution %s: %w", planningExecutionID, err)
	}

	roster := tui.NewPlanningRoster(store)
	model := tui.NewPlanningModel(roster, pe.FeatureID, 0)
	model.SetFeed(tui.NewTranscriptFeed(store))
	model.Approver = &planningapprove.Approver{Store: store, Artifacts: &fileArtifactLoader{RepoRoot: repoRoot}, Locks: repolock.New(repoRoot)}
	model.Answerer = answerer
	return model, nil
}

// runPlanningRoster drives the planning-phase Bubble Tea view for
// planningExecutionID until it quits, mirroring runLiveRoster.
func runPlanningRoster(ctx context.Context, store *storage.SQLiteStore, planningExecutionID string, answerer tui.Answerer, repoRoot string) error {
	model, err := buildPlanningModel(ctx, store, planningExecutionID, answerer, repoRoot)
	if err != nil {
		return err
	}
	model.SetContext(ctx)
	defer model.Close()

	p := tea.NewProgram(model, tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("forge: run planning roster: %w", err)
	}
	return nil
}
