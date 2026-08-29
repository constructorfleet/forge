package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

func TestFileArtifactLoader_SaveGoal_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	featureID := "widget"
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}
	goal.Revision = planning.ComputeRevision(goal)

	loader := &fileArtifactLoader{featureID: featureID}
	if err := loader.SaveGoal(context.Background(), featureID, goal); err != nil {
		t.Fatalf("SaveGoal: %v", err)
	}

	path := filepath.Join(dir, ".forge", "features", featureID, "goal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if want := planning.Render(goal); !reflect.DeepEqual(data, want) {
		t.Fatalf("file bytes = %q, want %q", data, want)
	}

	loaded, err := loader.LoadGoal(context.Background(), featureID)
	if err != nil {
		t.Fatalf("LoadGoal: %v", err)
	}
	if loaded.Kind != goal.Kind {
		t.Errorf("Kind = %q, want %q", loaded.Kind, goal.Kind)
	}
	if loaded.Revision != goal.Revision {
		t.Errorf("Revision = %q, want %q", loaded.Revision, goal.Revision)
	}
	if !reflect.DeepEqual(loaded.Sections, goal.Sections) {
		t.Errorf("Sections = %+v, want %+v", loaded.Sections, goal.Sections)
	}
}

func TestFileArtifactLoader_SaveGoal_CreatesFeatureDir(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	featureID := "new-feature"
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Something new"}},
	}
	goal.Revision = planning.ComputeRevision(goal)

	loader := &fileArtifactLoader{featureID: featureID}
	if err := loader.SaveGoal(context.Background(), featureID, goal); err != nil {
		t.Fatalf("SaveGoal: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".forge", "features", featureID, "goal.md")); err != nil {
		t.Fatalf("expected goal.md to exist: %v", err)
	}
}
