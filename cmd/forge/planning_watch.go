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
	return buildPlanningModelForFeature(store, pe.FeatureID, answerer, repoRoot), nil
}

// buildPlanningModelForFeature wires a PlanningModel directly over a Feature
// id, with no PlanningExecution row required. `forge watch` reaches this
// when an id resolves as a bare Feature (issue #485's third id-space probe:
// a Feature that was planned but never has, or has outlived, a
// planning_executions row).
func buildPlanningModelForFeature(store *storage.SQLiteStore, featureID string, answerer tui.Answerer, repoRoot string) *tui.PlanningModel {
	roster := tui.NewPlanningRoster(store)
	model := tui.NewPlanningModel(roster, featureID, 0)
	model.SetFeed(tui.NewTranscriptFeed(store))
	model.Approver = &planningapprove.Approver{Store: store, Artifacts: &fileArtifactLoader{RepoRoot: repoRoot}, Locks: repolock.New(repoRoot)}
	model.Answerer = answerer
	return model
}

// runPlanningRoster drives the planning-phase Bubble Tea view for
// planningExecutionID until it quits, mirroring runLiveRoster.
func runPlanningRoster(ctx context.Context, store *storage.SQLiteStore, planningExecutionID string, answerer tui.Answerer, repoRoot string) error {
	model, err := buildPlanningModel(ctx, store, planningExecutionID, answerer, repoRoot)
	if err != nil {
		return err
	}
	return runPlanningModel(ctx, model)
}

// runPlanningRosterForFeature drives the planning-phase Bubble Tea view
// directly over a Feature id, the `forge watch` counterpart to
// runPlanningRoster for an id that resolved as a bare Feature.
func runPlanningRosterForFeature(ctx context.Context, store *storage.SQLiteStore, featureID string, answerer tui.Answerer, repoRoot string) error {
	model := buildPlanningModelForFeature(store, featureID, answerer, repoRoot)
	return runPlanningModel(ctx, model)
}

// runPlanningModel runs model to completion in its own Bubble Tea program.
func runPlanningModel(ctx context.Context, model *tui.PlanningModel) error {
	model.SetContext(ctx)
	defer model.Close()

	p := tea.NewProgram(model, tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("forge: run planning roster: %w", err)
	}
	return nil
}
