package remote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/execution"
)

var (
	ErrWorkerAuthFailed = errors.New("remote: worker authentication failed")
	ErrWorkerNotFound   = errors.New("remote: worker not found")
	ErrNoEligibleWorker = errors.New("remote: no eligible worker")
)

type WorkerState string

const (
	WorkerStateAvailable WorkerState = "AVAILABLE"
	WorkerStateBusy      WorkerState = "BUSY"
	WorkerStateDraining  WorkerState = "DRAINING"
	WorkerStateOffline   WorkerState = "OFFLINE"
)

type ResourceCapacity struct {
	CPU      int
	MemoryMB int
	Slots    int
}

type ResourceLoad struct {
	CPU      int
	MemoryMB int
	Slots    int
}

type WorkerCapabilities struct {
	AgentBackends    []string
	ContainerCapable bool
	Capacity         ResourceCapacity
	Labels           map[string]string
}

type ExecutionRequirements struct {
	AgentBackend      string
	RequiresContainer bool
	Resources         ResourceCapacity
	RequiredLabels    map[string]string
}

type WorkerSnapshot struct {
	ID           string
	State        WorkerState
	Capabilities WorkerCapabilities
	Load         ResourceLoad
	LastSeenAt   time.Time
	ExpiresAt    time.Time
}

type WorkerRegistration struct {
	ID           string
	AuthToken    string
	Client       WorkerClient
	Capabilities WorkerCapabilities
	Load         ResourceLoad
}

type RegistryConfig struct {
	AuthToken string
	TTL       time.Duration
	Now       func() time.Time
}

type WorkerRegistry struct {
	authToken string
	ttl       time.Duration
	now       func() time.Time

	mu      sync.Mutex
	workers map[string]registeredWorker
}

type registeredWorker struct {
	client       WorkerClient
	capabilities WorkerCapabilities
	load         ResourceLoad
	state        WorkerState
	lastSeenAt   time.Time
	expiresAt    time.Time
}

func NewWorkerRegistry(cfg RegistryConfig) *WorkerRegistry {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	return &WorkerRegistry{
		authToken: cfg.AuthToken,
		ttl:       ttl,
		now:       now,
		workers:   make(map[string]registeredWorker),
	}
}

func (r *WorkerRegistry) Register(_ context.Context, reg WorkerRegistration) error {
	if strings.TrimSpace(reg.ID) == "" {
		return errors.New("remote: worker id must not be empty")
	}
	if r.authToken != "" && reg.AuthToken != r.authToken {
		return ErrWorkerAuthFailed
	}
	if reg.Client == nil {
		return errors.New("remote: worker client must not be nil")
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers[reg.ID] = registeredWorker{
		client:       reg.Client,
		capabilities: cloneCapabilities(reg.Capabilities),
		load:         reg.Load,
		state:        stateForLoad(reg.Capabilities.Capacity, reg.Load),
		lastSeenAt:   now,
		expiresAt:    now.Add(r.ttl),
	}
	return nil
}

func (r *WorkerRegistry) Heartbeat(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[id]
	if !ok {
		return ErrWorkerNotFound
	}
	now := r.now()
	worker.lastSeenAt = now
	worker.expiresAt = now.Add(r.ttl)
	if worker.state == WorkerStateOffline {
		worker.state = stateForLoad(worker.capabilities.Capacity, worker.load)
	}
	r.workers[id] = worker
	return nil
}

func (r *WorkerRegistry) Drain(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[id]
	if !ok {
		return
	}
	worker.state = WorkerStateDraining
	r.workers[id] = worker
}

func (r *WorkerRegistry) Deregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.workers, id)
}

func (r *WorkerRegistry) Snapshot(now time.Time) []WorkerSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]WorkerSnapshot, 0, len(r.workers))
	for id, worker := range r.workers {
		state := worker.state
		if !worker.expiresAt.IsZero() && now.After(worker.expiresAt) {
			state = WorkerStateOffline
		}
		out = append(out, WorkerSnapshot{
			ID:           id,
			State:        state,
			Capabilities: cloneCapabilities(worker.capabilities),
			Load:         worker.load,
			LastSeenAt:   worker.lastSeenAt,
			ExpiresAt:    worker.expiresAt,
		})
	}
	return out
}

func (r *WorkerRegistry) client(id string) (WorkerClient, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[id]
	if !ok {
		return nil, false
	}
	return worker.client, true
}

func PlaceWorker(workers []WorkerSnapshot, req ExecutionRequirements, now time.Time) (WorkerSnapshot, bool) {
	eligible := make([]WorkerSnapshot, 0, len(workers))
	for _, worker := range workers {
		if workerEligible(worker, req, now) {
			eligible = append(eligible, worker)
		}
	}
	if len(eligible) == 0 {
		return WorkerSnapshot{}, false
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left, right := eligible[i], eligible[j]
		if left.Load.Slots != right.Load.Slots {
			return left.Load.Slots < right.Load.Slots
		}
		if left.Load.CPU != right.Load.CPU {
			return left.Load.CPU < right.Load.CPU
		}
		if left.Load.MemoryMB != right.Load.MemoryMB {
			return left.Load.MemoryMB < right.Load.MemoryMB
		}
		return left.ID < right.ID
	})
	return eligible[0], true
}

func workerEligible(worker WorkerSnapshot, req ExecutionRequirements, now time.Time) bool {
	if worker.State != WorkerStateAvailable {
		return false
	}
	if !worker.ExpiresAt.IsZero() && now.After(worker.ExpiresAt) {
		return false
	}
	if req.AgentBackend != "" && !contains(worker.Capabilities.AgentBackends, req.AgentBackend) {
		return false
	}
	if req.RequiresContainer && !worker.Capabilities.ContainerCapable {
		return false
	}
	for key, value := range req.RequiredLabels {
		if worker.Capabilities.Labels[key] != value {
			return false
		}
	}
	return hasCapacity(worker.Capabilities.Capacity, worker.Load, req.Resources)
}

func hasCapacity(cap ResourceCapacity, load ResourceLoad, req ResourceCapacity) bool {
	return remaining(cap.CPU, load.CPU, req.CPU) &&
		remaining(cap.MemoryMB, load.MemoryMB, req.MemoryMB) &&
		remaining(cap.Slots, load.Slots, req.Slots)
}

func remaining(cap, used, need int) bool {
	if need <= 0 {
		return true
	}
	if cap <= 0 {
		return false
	}
	return cap-used >= need
}

func stateForLoad(cap ResourceCapacity, load ResourceLoad) WorkerState {
	if (cap.Slots > 0 && load.Slots >= cap.Slots) ||
		(cap.CPU > 0 && load.CPU >= cap.CPU) ||
		(cap.MemoryMB > 0 && load.MemoryMB >= cap.MemoryMB) {
		return WorkerStateBusy
	}
	return WorkerStateAvailable
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneCapabilities(caps WorkerCapabilities) WorkerCapabilities {
	out := caps
	out.AgentBackends = append([]string(nil), caps.AgentBackends...)
	if caps.Labels != nil {
		out.Labels = make(map[string]string, len(caps.Labels))
		for key, value := range caps.Labels {
			out.Labels[key] = value
		}
	}
	return out
}

type RequirementsFunc func(execution.WorkspaceRequest) ExecutionRequirements

func StaticRequirements(req ExecutionRequirements) RequirementsFunc {
	return func(execution.WorkspaceRequest) ExecutionRequirements {
		return req
	}
}

type PoolBackend struct {
	registry     *WorkerRegistry
	requirement  RequirementsFunc
	recover      RecoverFunc
	leases       LeaseStore
	now          func() time.Time
	heartbeatTTL time.Duration
}

func NewPoolBackend(registry *WorkerRegistry, requirement RequirementsFunc, recover RecoverFunc, leases LeaseStore) *PoolBackend {
	now := time.Now
	if registry != nil && registry.now != nil {
		now = registry.now
	}
	return &PoolBackend{
		registry:     registry,
		requirement:  requirement,
		recover:      recover,
		leases:       leases,
		now:          now,
		heartbeatTTL: defaultLeaseTTL,
	}
}

func (b *PoolBackend) Prepare(ctx context.Context, req execution.WorkspaceRequest) (execution.ExecutionEnvironment, error) {
	if b.registry == nil {
		return nil, errors.New("remote: worker registry must not be nil")
	}
	requirements := ExecutionRequirements{}
	if b.requirement != nil {
		requirements = b.requirement(req)
	}
	selected, ok := PlaceWorker(b.registry.Snapshot(b.now()), requirements, b.now())
	if !ok {
		return nil, fmt.Errorf("%w for %s/%s", ErrNoEligibleWorker, req.ExecutionID, req.IssueID)
	}
	worker, ok := b.registry.client(selected.ID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWorkerNotFound, selected.ID)
	}
	backend := NewBackendWithLeases(worker, b.recover, b.leases)
	backend.workerRef = selected.ID
	backend.ttl = b.heartbeatTTL
	return backend.Prepare(ctx, req)
}

var _ execution.ExecutionBackend = (*PoolBackend)(nil)
