// Package planningfs implements the filesystem-backed Planning Artifact
// loader: reading and writing the goal, decision, spec, and ticket-plan
// files under .forge/features/<feature-id>. It is the reusable form of the
// loader cmd/forge's plan, goal, and approve commands share, and the same
// loader internal/planningapprove uses to find and approve a Feature's
// pending Planning Artifact.
package planningfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Teagan42/forge/internal/planning"
)

// FileArtifactLoader reads and writes a Feature's Planning Artifacts under
// .forge/features/<feature-id>, relative to the process's current working
// directory. It carries no state: every method takes the Feature ID it
// operates on.
type FileArtifactLoader struct{}

// LoadGoal reads and parses the Feature's goal Artifact.
func (f *FileArtifactLoader) LoadGoal(ctx context.Context, featureID string) (*planning.Artifact, error) {
	path := filepath.Join(".forge", "features", featureID, "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return planning.Parse(data)
}

// SaveGoal renders and writes the Feature's goal Artifact, creating the
// Feature's directory on first use.
func (f *FileArtifactLoader) SaveGoal(ctx context.Context, featureID string, goal *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "goal.md")
	data := planning.Render(goal)
	return os.WriteFile(path, data, 0o644)
}

// LoadDecisions reads and parses every recorded Decision Artifact for the
// Feature, keyed by decision ID (the NNN-slug filename stem). A Feature with
// no decisions directory yet returns an empty map, not an error.
func (f *FileArtifactLoader) LoadDecisions(ctx context.Context, featureID string) (map[string]*planning.Artifact, error) {
	dir := filepath.Join(".forge", "features", featureID, "decisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]*planning.Artifact{}, nil
		}
		return nil, err
	}

	decisions := make(map[string]*planning.Artifact)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-3]
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		artifact, err := planning.Parse(data)
		if err != nil {
			return nil, err
		}
		decisions[id] = artifact
	}
	return decisions, nil
}

// SaveDecision writes one Decision Artifact back to
// .forge/features/<feature>/decisions/<id>.md, creating the directory on
// the Feature's first Decision.
func (f *FileArtifactLoader) SaveDecision(ctx context.Context, featureID, decisionID string, decision *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID, "decisions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, decisionID+".md"), planning.Render(decision), 0o644)
}

// SaveSpec renders and writes the Feature's spec Artifact.
func (f *FileArtifactLoader) SaveSpec(ctx context.Context, featureID string, spec *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "spec.md")
	data := planning.Render(spec)
	return os.WriteFile(path, data, 0o644)
}

// LoadSpec reads and parses the Feature's spec Artifact. A Feature with no
// spec yet returns a nil Artifact, not an error.
func (f *FileArtifactLoader) LoadSpec(ctx context.Context, featureID string) (*planning.Artifact, error) {
	path := filepath.Join(".forge", "features", featureID, "spec.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return planning.Parse(data)
}

// LoadTicketPlan reads and parses the Feature's ticket-plan Artifact. A
// Feature with no ticket plan yet returns a nil Artifact, not an error.
func (f *FileArtifactLoader) LoadTicketPlan(ctx context.Context, featureID string) (*planning.Artifact, error) {
	path := filepath.Join(".forge", "features", featureID, "ticket-plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return planning.Parse(data)
}

// SaveTicketPlan renders and writes the Feature's ticket-plan Artifact.
func (f *FileArtifactLoader) SaveTicketPlan(ctx context.Context, featureID string, tp *planning.Artifact) error {
	dir := filepath.Join(".forge", "features", featureID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "ticket-plan.md")
	data := planning.Render(tp)
	return os.WriteFile(path, data, 0o644)
}
