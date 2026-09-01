package container

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Teagan42/forge/internal/execution"
)

// errExecNotSupported is FakeRuntime's Exec outcome. Command execution
// inside a container ships in a later ticket; the fake records nothing to
// simulate for it yet.
var errExecNotSupported = errors.New("container: FakeRuntime.Exec is not supported yet")

// FakeRuntime is a deterministic in-memory ContainerRuntime for tests: it
// simulates start/stop/remove without a live container daemon and records
// every call so tests can assert on them.
type FakeRuntime struct {
	mu      sync.Mutex
	nextID  int
	started []ContainerSpec
	stopped []ContainerHandle
	removed []ContainerHandle
}

// NewFakeRuntime returns an empty FakeRuntime.
func NewFakeRuntime() *FakeRuntime {
	return &FakeRuntime{}
}

// Start records spec and returns a freshly minted handle for it.
func (r *FakeRuntime) Start(_ context.Context, spec ContainerSpec) (ContainerHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.started = append(r.started, spec)
	return ContainerHandle(fmt.Sprintf("fake-container-%d", r.nextID)), nil
}

// Exec always fails: this ticket's FakeRuntime only simulates the
// start/stop/remove lifecycle Prepare and Cleanup need.
func (r *FakeRuntime) Exec(_ context.Context, _ ContainerHandle, _ execution.Command) (execution.Result, error) {
	return execution.Result{}, errExecNotSupported
}

// Stop records handle as stopped.
func (r *FakeRuntime) Stop(_ context.Context, handle ContainerHandle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, handle)
	return nil
}

// Remove records handle as removed.
func (r *FakeRuntime) Remove(_ context.Context, handle ContainerHandle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, handle)
	return nil
}

// Started returns every ContainerSpec passed to Start so far, in call order.
func (r *FakeRuntime) Started() []ContainerSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContainerSpec, len(r.started))
	copy(out, r.started)
	return out
}

// Stopped returns every ContainerHandle passed to Stop so far, in call
// order.
func (r *FakeRuntime) Stopped() []ContainerHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContainerHandle, len(r.stopped))
	copy(out, r.stopped)
	return out
}

// Removed returns every ContainerHandle passed to Remove so far, in call
// order.
func (r *FakeRuntime) Removed() []ContainerHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ContainerHandle, len(r.removed))
	copy(out, r.removed)
	return out
}

var _ ContainerRuntime = (*FakeRuntime)(nil)
