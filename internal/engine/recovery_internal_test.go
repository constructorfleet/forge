package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/storage"
)

// workspaceSpyStore is a minimal in-package storage.Store double covering
// only what ensureWorkspace touches.
type workspaceSpyStore struct {
	storage.Store // embed to satisfy Store; only the overridden methods are called

	recorded []domain.Workspace
	events   []storage.Event
}

func (s *workspaceSpyStore) RecordWorkspace(_ context.Context, _ string, ws domain.Workspace) error {
	s.recorded = append(s.recorded, ws)
	return nil
}

func (s *workspaceSpyStore) AppendEvent(_ context.Context, event storage.Event) error {
	s.events = append(s.events, event)
	return nil
}

// unhealthyWorkspaceCreator is a WorkspaceCreator whose Workspace fails
// validation, which is the state ensureWorkspace must recover from. It also
// records a direct Create call, so a test can prove the Engine no longer
// makes one.
type unhealthyWorkspaceCreator struct {
	cleanupCalled bool
	createCalled  bool
}

func (c *unhealthyWorkspaceCreator) Create(context.Context, string, string, string) (domain.Workspace, error) {
	c.createCalled = true
	return domain.Workspace{}, nil
}

func (c *unhealthyWorkspaceCreator) Cleanup(context.Context, string, string) error {
	c.cleanupCalled = true
	return nil
}

func (c *unhealthyWorkspaceCreator) Validate(context.Context, string, string) (domain.Workspace, error) {
	return domain.Workspace{}, errors.New("workspace directory is gone")
}

// healthyWorkspaceCreator is a WorkspaceCreator whose Workspace validates
// cleanly, so ensureWorkspace must skip Cleanup and go straight to the
// backend.
type healthyWorkspaceCreator struct {
	cleanupCalled bool
	createCalled  bool
}

func (c *healthyWorkspaceCreator) Create(context.Context, string, string, string) (domain.Workspace, error) {
	c.createCalled = true
	return domain.Workspace{}, nil
}

func (c *healthyWorkspaceCreator) Cleanup(context.Context, string, string) error {
	c.cleanupCalled = true
	return nil
}

func (c *healthyWorkspaceCreator) Validate(context.Context, string, string) (domain.Workspace, error) {
	return domain.Workspace{Path: "/tmp/ws", Branch: "worker/42"}, nil
}

// recordingBackend is an ExecutionBackend double that records the request
// it prepared.
type recordingBackend struct {
	prepared  execution.WorkspaceRequest
	workspace domain.Workspace
}

func (b *recordingBackend) Prepare(_ context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	b.prepared = req
	return &workspaceEnvironment{
		executionID: req.ExecutionID,
		issueID:     req.IssueID,
		workspace:   b.workspace,
	}, nil
}

var _ execution.ExecutionBackend = (*recordingBackend)(nil)

// TestEnsureWorkspace_RecreatesThroughTheBackend covers ticket 305's
// acceptance criterion for the recovery path: the Engine replaces an
// unhealthy Workspace through the backend, not through a direct
// WorkspaceCreator.Create call.
func TestEnsureWorkspace_RecreatesThroughTheBackend(t *testing.T) {
	creator := &unhealthyWorkspaceCreator{}
	backend := &recordingBackend{workspace: domain.Workspace{Path: "/tmp/ws", Branch: "worker/42"}}
	store := &workspaceSpyStore{}
	e := &Engine{
		Store:      store,
		Workspaces: creator,
		Backend:    backend,
		Now:        func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}

	env, err := e.ensureWorkspace(context.Background(), "exec-1", "42", "abc123")
	if err != nil {
		t.Fatalf("ensureWorkspace: %v", err)
	}

	ws := env.Workspace()
	if ws.Path != "/tmp/ws" || ws.Branch != "worker/42" {
		t.Errorf("Workspace = %+v, want the one the backend prepared", ws)
	}
	if creator.createCalled {
		t.Error("ensureWorkspace called WorkspaceCreator.Create directly, want the backend")
	}
	if !creator.cleanupCalled {
		t.Error("ensureWorkspace did not clean the unhealthy Workspace up first")
	}
	want := execution.WorkspaceRequest{ExecutionID: "exec-1", IssueID: "42", Base: "abc123"}
	if backend.prepared != want {
		t.Errorf("Prepare called with %+v, want %+v", backend.prepared, want)
	}
	if len(store.recorded) != 1 || store.recorded[0] != ws {
		t.Errorf("recorded workspaces = %+v, want exactly the prepared one", store.recorded)
	}
	if len(store.events) != 1 || store.events[0].Type != "workspace.recovered" {
		t.Errorf("events = %+v, want one workspace.recovered event", store.events)
	}
}

// TestEnsureWorkspace_HealthyWorkspaceStillGoesThroughTheBackend covers
// ticket 305's other half: even a Workspace Validate finds healthy is
// re-prepared through the backend rather than trusted as-is, so the
// environment stays the Engine's only path to a Workspace. It records no
// workspace.recovered Event and never calls Cleanup, since nothing needed
// recovering.
func TestEnsureWorkspace_HealthyWorkspaceStillGoesThroughTheBackend(t *testing.T) {
	creator := &healthyWorkspaceCreator{}
	backend := &recordingBackend{workspace: domain.Workspace{Path: "/tmp/ws", Branch: "worker/42"}}
	store := &workspaceSpyStore{}
	e := &Engine{
		Store:      store,
		Workspaces: creator,
		Backend:    backend,
		Now:        func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}

	env, err := e.ensureWorkspace(context.Background(), "exec-1", "42", "abc123")
	if err != nil {
		t.Fatalf("ensureWorkspace: %v", err)
	}

	ws := env.Workspace()
	if ws.Path != "/tmp/ws" || ws.Branch != "worker/42" {
		t.Errorf("Workspace = %+v, want the one the backend prepared", ws)
	}
	if creator.createCalled {
		t.Error("ensureWorkspace called WorkspaceCreator.Create directly, want the backend")
	}
	if creator.cleanupCalled {
		t.Error("ensureWorkspace cleaned up a healthy Workspace, want no Cleanup")
	}
	want := execution.WorkspaceRequest{ExecutionID: "exec-1", IssueID: "42", Base: "abc123"}
	if backend.prepared != want {
		t.Errorf("Prepare called with %+v, want %+v", backend.prepared, want)
	}
	if len(store.recorded) != 1 || store.recorded[0] != ws {
		t.Errorf("recorded workspaces = %+v, want exactly the prepared one", store.recorded)
	}
	if len(store.events) != 0 {
		t.Errorf("events = %+v, want none (nothing needed recovering)", store.events)
	}
}
