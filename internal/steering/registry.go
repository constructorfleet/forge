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
// No production code calls Register or Unregister yet: internal/engine
// does not register a running loop's Queue into DefaultRegistry, and
// forge execute and a separately-invoked forge steer run as distinct OS
// processes with independent DefaultRegistry instances in any case, so
// even same-process registration would not let `forge steer` reach a loop
// started by a different forge execute invocation. Wiring registration,
// and deciding whether cross-process delivery needs a store-backed relay
// (the pattern forge cancel uses), is tracked in
// constructorfleet/forge#746.
type Registry struct {
	mu    sync.Mutex
	loops map[string]*Queue
}

// NewRegistry returns an empty Registry ready for concurrent use.
func NewRegistry() *Registry {
	return &Registry{loops: make(map[string]*Queue)}
}

// DefaultRegistry is the process-wide Registry `forge steer` resolves loop
// identifiers through. See the Registry doc comment: nothing registers a
// running execute loop's Queue here yet (constructorfleet/forge#746), so
// DefaultRegistry is empty in every real forge invocation today.
var DefaultRegistry = NewRegistry()

// Register attaches queue under loopID. A later Steer call for that loopID
// enqueues onto queue.
func (r *Registry) Register(loopID string, queue *Queue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loops[loopID] = queue
}

// Unregister removes loopID, e.g. once its execute loop finishes. A later
// Steer call for that loopID then returns ErrLoopNotFound.
func (r *Registry) Unregister(loopID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.loops, loopID)
}

// Steer resolves loopID to its registered Queue and enqueues a Message built
// from text. It returns ErrLoopNotFound if no loop is registered under
// loopID. Steer never waits on step completion: it returns as soon as the
// underlying Queue.Enqueue call returns, which itself never blocks on the
// step-execution goroutine.
func (r *Registry) Steer(loopID, text string) error {
	r.mu.Lock()
	queue, ok := r.loops[loopID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrLoopNotFound, loopID)
	}
	queue.Enqueue(Message{Text: text})
	return nil
}
