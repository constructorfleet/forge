package specengine

import (
	"context"
	"fmt"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/spec"
	"github.com/Teagan42/forge/internal/specgeneration"
)

type SpecEngine struct {
	Backend planningagent.Backend
}

func NewSpecEngine(backend planningagent.Backend) *SpecEngine {
	return &SpecEngine{Backend: backend}
}

type ArtifactLoader interface {
	LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error)
	LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error)
	SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error
}

func (e *SpecEngine) GenerateSpec(ctx context.Context, featureID string, loader ArtifactLoader) error {
	goalArtifact, err := loader.LoadGoal(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load goal: %w", err)
	}

	decisions, err := loader.LoadDecisions(ctx, featureID)
	if err != nil {
		return fmt.Errorf("specengine: load decisions: %w", err)
	}

	for id, dec := range decisions {
		if dec.State == "open" || dec.State == "needs_human" {
			return fmt.Errorf("specengine: decision %s is not resolved", id)
		}
	}

	artifacts := []planningagent.NamedArtifact{
		{ID: "goal", Artifact: goalArtifact},
	}
	for id, dec := range decisions {
		artifacts = append(artifacts, planningagent.NamedArtifact{ID: id, Artifact: dec})
	}
	pc, err := planningagent.Compile(agent.RepositoryContext{BaseRevision: "base"}, artifacts, nil)
	if err != nil {
		return fmt.Errorf("specengine: compile planning context: %w", err)
	}

	specResult, err := specgeneration.Generate(ctx, e.Backend, pc)
	if err != nil {
		return fmt.Errorf("specengine: generate spec: %w", err)
	}

	specArtifact := spec.NewSpecification()
	specArtifact.AddSection("Context", specResult.Summary)

	reqBody := ""
	for _, req := range specResult.Requirements {
		reqBody += fmt.Sprintf("%s: %s\n", req.ID, req.Description)
	}
	specArtifact.AddSection("Requirements", reqBody)

	nonGoalsBody := ""
	for _, ng := range specResult.NonGoals {
		nonGoalsBody += fmt.Sprintf("- %s\n", ng)
	}
	specArtifact.AddSection("Non-Goals", nonGoalsBody)

	decisionRevs := make(map[string]string)
	for id, dec := range decisions {
		decisionRevs[id] = dec.Revision
	}

	specArtifact.DerivedFrom = []planning.DerivedFromEntry{
		{Kind: planning.KindGoal, ID: "goal", Revision: goalArtifact.Revision},
	}
	for id, dec := range decisions {
		specArtifact.DerivedFrom = append(specArtifact.DerivedFrom, planning.DerivedFromEntry{
			Kind: planning.KindDecision, ID: id, Revision: dec.Revision,
		})
	}
	specArtifact.DerivedFrom = append(specArtifact.DerivedFrom, planning.DerivedFromEntry{
		Kind: "repository", ID: "repository", Revision: pc.ContextRevision,
	})

	specArtifact.Revision = planning.ComputeRevision(&specArtifact.Artifact)

	if err := spec.ValidateSpecDeterministic(&specArtifact.Artifact, decisions, goalArtifact.Revision, decisionRevs, pc.ContextRevision); err != nil {
		return fmt.Errorf("specengine: deterministic validation failed: %w", err)
	}

	if err := loader.SaveSpec(ctx, featureID, &specArtifact.Artifact); err != nil {
		return fmt.Errorf("specengine: save spec: %w", err)
	}

	return nil
}

type fakeLoader struct {
	goal      *planning.Artifact
	decisions map[string]*planning.Artifact
	spec      *planning.Artifact
}

func (f *fakeLoader) LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error) {
	if f.goal == nil {
		return nil, fmt.Errorf("goal not found")
	}
	return f.goal, nil
}

func (f *fakeLoader) LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error) {
	return f.decisions, nil
}

func (f *fakeLoader) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	f.spec = spec
	return nil
}
