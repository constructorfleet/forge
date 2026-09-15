package steering

import (
	"errors"
	"fmt"
	"sync"
)

// ErrLoopNotFound reports that no running loop is registered under a given
// loop identifier.
var ErrLoopNotFound = errors.New("steering: loop not found")

// Registry resolves a loop identifier to the Queue attached to that loop's
// execute session. A caller with a Queue reference calls Register under a
// loop identifier, so a separate caller (the `forge steer` CLI command, an
// RPC handler) can later reach the right Queue by loop identifier alone,
// without holding a reference to the loop itself.
//
// internal/engine's ExecuteInExecution registers a running loop's Queue
// into DefaultRegistry under its Execution ID (constructorfleet/forge#746),
// so `forge steer <execution-id>` reaches a loop running in the same
// process. forge execute and a separately-invoked forge steer still run as
// distinct OS processes with independent DefaultRegistry instances, so
// forge steer cannot yet reach a loop started by a different forge execute
// invocation; whether cross-process delivery needs a store-backed relay
// (the pattern forge cancel uses) is left to a future ticket.
type Registry struct {
	mu    sync.Mutex
	loops map[string]*registration
}

// registration tracks a registered Queue alongside a reference count, so
// concurrent Workers that share one loop-id (one Execution's concurrently
// dispatched Issues, all registering the same Queue under the same
// Execution ID) can each Register and Unregister independently: the
// loop-id stays registered until every outstanding Register call for it
// has a matching Unregister.
type registration struct {
	queue *Queue
	count int
}

// NewRegistry returns an empty Registry ready for concurrent use.
func NewRegistry() *Registry {
	return &Registry{loops: make(map[string]*registration)}
}

// DefaultRegistry is the process-wide Registry `forge steer` resolves loop
// identifiers through. See the Registry doc comment: internal/engine
// registers a running execute loop's Queue here under its Execution ID.
var DefaultRegistry = NewRegistry()

// Register attaches queue under loopID. A later Steer call for that loopID
// enqueues onto queue. Register is reference-counted: calling it more than
// once for the same loopID (concurrent Workers sharing one Execution's
// loop-id) requires a matching number of Unregister calls before loopID is
// removed.
func (r *Registry) Register(loopID string, queue *Queue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reg, ok := r.loops[loopID]; ok {
		reg.count++
		return
	}
	r.loops[loopID] = &registration{queue: queue, count: 1}
}

// Unregister releases one Register call for loopID. It removes loopID once
// every outstanding Register call for it has a matching Unregister, e.g.
// once every concurrent Worker sharing that loop-id has finished. A later
// Steer call for a fully unregistered loopID then returns ErrLoopNotFound.
func (r *Registry) Unregister(loopID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg, ok := r.loops[loopID]
	if !ok {
		return
	}
	reg.count--
	if reg.count <= 0 {
		delete(r.loops, loopID)
	}
}

// Steer resolves loopID to its registered Queue and enqueues a Message built
// from text. It returns ErrLoopNotFound if no loop is registered under
// loopID. Steer never waits on step completion: it returns as soon as the
// underlying Queue.Enqueue call returns, which itself never blocks on the
// step-execution goroutine.
func (r *Registry) Steer(loopID, text string) error {
	r.mu.Lock()
	reg, ok := r.loops[loopID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrLoopNotFound, loopID)
	}
	reg.queue.Enqueue(Message{Text: text})
	return nil
}
