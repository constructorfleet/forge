package remote

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

func TestPlaceWorkerFiltersByCapabilitiesAndState(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	snapshot := []WorkerSnapshot{
		{
			ID:    "offline",
			State: WorkerStateOffline,
			Capabilities: WorkerCapabilities{
				AgentBackends:    []string{"codex"},
				ContainerCapable: true,
				Capacity:         ResourceCapacity{CPU: 8, MemoryMB: 16384, Slots: 4},
				Labels:           map[string]string{"region": "us-west", "gpu": "true"},
			},
		},
		{
			ID:    "wrong-backend",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends:    []string{"claude-code"},
				ContainerCapable: true,
				Capacity:         ResourceCapacity{CPU: 8, MemoryMB: 16384, Slots: 4},
				Labels:           map[string]string{"region": "us-west", "gpu": "true"},
			},
		},
		{
			ID:    "busy",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends:    []string{"codex"},
				ContainerCapable: true,
				Capacity:         ResourceCapacity{CPU: 4, MemoryMB: 8192, Slots: 1},
				Labels:           map[string]string{"region": "us-west", "gpu": "true"},
			},
			Load: ResourceLoad{CPU: 4, MemoryMB: 8192, Slots: 1},
		},
		{
			ID:    "selected",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends:    []string{"codex", "opencode"},
				ContainerCapable: true,
				Capacity:         ResourceCapacity{CPU: 8, MemoryMB: 16384, Slots: 4},
				Labels:           map[string]string{"region": "us-west", "gpu": "true"},
			},
			Load: ResourceLoad{CPU: 2, MemoryMB: 1024, Slots: 1},
		},
		{
			ID:    "missing-label",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends:    []string{"codex"},
				ContainerCapable: true,
				Capacity:         ResourceCapacity{CPU: 8, MemoryMB: 16384, Slots: 4},
				Labels:           map[string]string{"region": "us-east"},
			},
		},
	}

	placed, ok := PlaceWorker(snapshot, ExecutionRequirements{
		AgentBackend:      "codex",
		RequiresContainer: true,
		Resources:         ResourceCapacity{CPU: 2, MemoryMB: 2048, Slots: 1},
		RequiredLabels:    map[string]string{"region": "us-west", "gpu": "true"},
	}, now)

	if !ok {
		t.Fatal("PlaceWorker ok = false, want a selected worker")
	}
	if placed.ID != "selected" {
		t.Fatalf("PlaceWorker selected %q, want selected", placed.ID)
	}
}

func TestPlaceWorkerUsesLeastLoadedThenStableID(t *testing.T) {
	snapshot := []WorkerSnapshot{
		{
			ID:    "worker-b",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends: []string{"codex"},
				Capacity:      ResourceCapacity{CPU: 8, MemoryMB: 8192, Slots: 4},
			},
			Load: ResourceLoad{CPU: 1, Slots: 1},
		},
		{
			ID:    "worker-a",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends: []string{"codex"},
				Capacity:      ResourceCapacity{CPU: 8, MemoryMB: 8192, Slots: 4},
			},
			Load: ResourceLoad{CPU: 1, Slots: 1},
		},
		{
			ID:    "worker-c",
			State: WorkerStateAvailable,
			Capabilities: WorkerCapabilities{
				AgentBackends: []string{"codex"},
				Capacity:      ResourceCapacity{CPU: 8, MemoryMB: 8192, Slots: 4},
			},
			Load: ResourceLoad{CPU: 2, Slots: 2},
		},
	}

	placed, ok := PlaceWorker(snapshot, ExecutionRequirements{AgentBackend: "codex", Resources: ResourceCapacity{Slots: 1}}, time.Now())

	if !ok {
		t.Fatal("PlaceWorker ok = false, want a selected worker")
	}
	if placed.ID != "worker-a" {
		t.Fatalf("PlaceWorker selected %q, want worker-a", placed.ID)
	}
}

func TestRegistryLifecycleRemovesWorkersFromPlacement(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	registry := NewWorkerRegistry(RegistryConfig{
		AuthToken: "secret",
		TTL:       time.Minute,
		Now:       func() time.Time { return now },
	})
	worker := NewFakeWorker(domain.Workspace{IssueID: "issue-42"})

	err := registry.Register(context.Background(), WorkerRegistration{
		ID:           "worker-1",
		AuthToken:    "wrong",
		Client:       worker,
		Capabilities: WorkerCapabilities{AgentBackends: []string{"codex"}, Capacity: ResourceCapacity{Slots: 1}},
	})
	if !errors.Is(err, ErrWorkerAuthFailed) {
		t.Fatalf("Register invalid token error = %v, want ErrWorkerAuthFailed", err)
	}

	if err := registry.Register(context.Background(), WorkerRegistration{
		ID:           "worker-1",
		AuthToken:    "secret",
		Client:       worker,
		Capabilities: WorkerCapabilities{AgentBackends: []string{"codex"}, Capacity: ResourceCapacity{Slots: 1}},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	snapshot := registry.Snapshot(now.Add(30 * time.Second))
	if len(snapshot) != 1 || snapshot[0].State != WorkerStateAvailable {
		t.Fatalf("Snapshot before timeout = %+v, want one available worker", snapshot)
	}

	registry.Drain("worker-1")
	_, ok := PlaceWorker(registry.Snapshot(now.Add(30*time.Second)), ExecutionRequirements{AgentBackend: "codex", Resources: ResourceCapacity{Slots: 1}}, now)
	if ok {
		t.Fatal("PlaceWorker ok = true for draining worker, want false")
	}

	if err := registry.Heartbeat(context.Background(), "worker-1"); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	snapshot = registry.Snapshot(now.Add(2 * time.Minute))
	if len(snapshot) != 1 || snapshot[0].State != WorkerStateOffline {
		t.Fatalf("Snapshot after timeout = %+v, want one offline worker", snapshot)
	}

	registry.Deregister("worker-1")
	if got := registry.Snapshot(now); len(got) != 0 {
		t.Fatalf("Snapshot after deregister = %+v, want none", got)
	}
}

func TestPoolBackendSelectsWorkerAndRunsRemotePath(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	registry := NewWorkerRegistry(RegistryConfig{Now: func() time.Time { return now }, TTL: time.Minute})
	workerA := NewFakeWorker(domain.Workspace{IssueID: "issue-42", Path: "/worker-a", Branch: "forge/exec1/issue-42"})
	workerB := NewFakeWorker(domain.Workspace{IssueID: "issue-42", Path: "/worker-b", Branch: "forge/exec1/issue-42"})
	mustRegisterWorker(t, registry, "worker-b", workerB, ResourceLoad{Slots: 1})
	mustRegisterWorker(t, registry, "worker-a", workerA, ResourceLoad{})
	leases := &fakeLeaseStore{}

	backend := NewPoolBackend(registry, StaticRequirements(ExecutionRequirements{
		AgentBackend: "codex",
		Resources:    ResourceCapacity{Slots: 1},
	}), nil, leases)

	env, err := backend.Prepare(context.Background(), execution.WorkspaceRequest{ExecutionID: "exec1", IssueID: "issue-42", Base: "deadbeef"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = env.Cleanup(context.Background()) })

	if got := workerA.Prepared(); len(got) != 1 {
		t.Fatalf("worker-a Prepared = %+v, want one prepare", got)
	}
	if got := workerB.Prepared(); len(got) != 0 {
		t.Fatalf("worker-b Prepared = %+v, want no prepare", got)
	}
	if len(leases.placements) != 1 || leases.placements[0].WorkerRef != "worker-a" {
		t.Fatalf("placement = %+v, want worker-a WorkerRef", leases.placements)
	}
}

func mustRegisterWorker(t *testing.T, registry *WorkerRegistry, id string, worker WorkerClient, load ResourceLoad) {
	t.Helper()
	if err := registry.Register(context.Background(), WorkerRegistration{
		ID:           id,
		Client:       worker,
		Capabilities: WorkerCapabilities{AgentBackends: []string{"codex"}, Capacity: ResourceCapacity{CPU: 8, MemoryMB: 8192, Slots: 4}},
		Load:         load,
	}); err != nil {
		t.Fatalf("Register %s: %v", id, err)
	}
}
