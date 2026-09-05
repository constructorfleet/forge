package planningfs

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Teagan42/forge/internal/planning"
)

func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("Chdir back: %v", err)
		}
	})
}

func TestFileArtifactLoader_SaveGoal_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	featureID := "widget"
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Build a widget"}},
	}
	goal.Revision = planning.ComputeRevision(goal)

	loader := &FileArtifactLoader{}
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

	loader := &FileArtifactLoader{}
	if err := loader.SaveGoal(context.Background(), featureID, goal); err != nil {
		t.Fatalf("SaveGoal: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".forge", "features", featureID, "goal.md")); err != nil {
		t.Fatalf("expected goal.md to exist: %v", err)
	}
}

func TestFileArtifactLoader_RepoRootControlsArtifactDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	sub := filepath.Join(repoRoot, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	chdirTemp(t, sub)

	featureID := "rooted-feature"
	goal := &planning.Artifact{
		Kind:     planning.KindGoal,
		Sections: []planning.Section{{Heading: "Goal", Body: "Use the repo root"}},
	}
	goal.Revision = planning.ComputeRevision(goal)

	loader := &FileArtifactLoader{RepoRoot: repoRoot}
	if err := loader.SaveGoal(context.Background(), featureID, goal); err != nil {
		t.Fatalf("SaveGoal: %v", err)
	}

	rootPath := filepath.Join(repoRoot, ".forge", "features", featureID, "goal.md")
	if _, err := os.Stat(rootPath); err != nil {
		t.Fatalf("expected goal.md under repo root: %v", err)
	}
	cwdPath := filepath.Join(sub, ".forge", "features", featureID, "goal.md")
	if _, err := os.Stat(cwdPath); !os.IsNotExist(err) {
		t.Fatalf("goal.md under cwd exists or failed unexpectedly: %v", err)
	}

	loaded, err := loader.LoadGoal(context.Background(), featureID)
	if err != nil {
		t.Fatalf("LoadGoal: %v", err)
	}
	if loaded.Revision != goal.Revision {
		t.Fatalf("Revision = %q, want %q", loaded.Revision, goal.Revision)
	}
}

func TestFileArtifactLoader_LoadSpec_MissingReturnsNilNoError(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	loader := &FileArtifactLoader{}
	spec, err := loader.LoadSpec(context.Background(), "no-such-feature")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if spec != nil {
		t.Fatalf("spec = %+v, want nil", spec)
	}
}

func TestFileArtifactLoader_SaveSpec_ApprovedRevisionRoundTrips(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)

	featureID := "widget"
	spec := &planning.Artifact{
		Kind:     planning.KindSpec,
		Sections: []planning.Section{{Heading: "Objective", Body: "Build a widget"}},
	}
	rev := planning.ComputeRevision(spec)
	spec.Revision = rev
	spec.ApprovedRevision = rev
	spec.State = "approved"

	loader := &FileArtifactLoader{}
	if err := loader.SaveSpec(context.Background(), featureID, spec); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}

	loaded, err := loader.LoadSpec(context.Background(), featureID)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if loaded.ApprovedRevision != rev {
		t.Errorf("ApprovedRevision = %q, want %q", loaded.ApprovedRevision, rev)
	}
	if loaded.State != "approved" {
		t.Errorf("State = %q, want %q", loaded.State, "approved")
	}
}
