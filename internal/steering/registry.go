package steering

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Teagan42/forge/internal/executeloop"
)

// ErrLoopNotFound reports that no running loop is registered under a given
// loop identifier.
var ErrLoopNotFound = errors.New("steering: loop not found")

// ErrStatusUnavailable reports that loopID is registered but was registered
// with no executeloop.Session (an Engine with Steering wired but Session
// unset), so its live running/needs_info status cannot be queried.
var ErrStatusUnavailable = errors.New("steering: loop status unavailable")

// Registry resolves a loop identifier to the Queue and executeloop.Session
// attached to that loop's execute session. A caller with a Queue and Session
// reference calls Register under a loop identifier, so a separate caller
// (the `forge steer` and `forge status` CLI commands, an RPC handler) can
// later reach the right Queue (Steer) or Session status (Status) by loop
// identifier alone, without holding a reference to the loop itself.
//
// internal/engine's ExecuteInExecution registers a running loop's Queue and
// Session into DefaultRegistry under its Execution ID (constructorfleet/
// forge#746, constructorfleet/forge#739), so `forge steer <execution-id>`
// and `forge status <execution-id>` reach a loop running in the same
// process. forge execute and a separately-invoked forge steer/forge status
// still run as distinct OS processes with independent DefaultRegistry
// instances, so neither can yet reach a loop started by a different forge
// execute invocation; whether cross-process delivery needs a store-backed
// relay (the pattern forge cancel uses) is left to a future ticket.
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
//
// session is the same loop's executeloop.Session, registered alongside its
// Queue (TKT-009, constructorfleet/forge#739) so a caller that already
// resolves loop-id -> Queue through this Registry (`forge steer`) can
// resolve loop-id -> running/needs_info status the same way, without a
// second lookup mechanism. It is nil when the registering Engine has no
// Session wired, in which case Status reports ErrStatusUnavailable rather
// than a guessed value.
type registration struct {
	queue   *Queue
	session *executeloop.Session
	count   int
}

// NewRegistry returns an empty Registry ready for concurrent use.
func NewRegistry() *Registry {
	return &Registry{loops: make(map[string]*registration)}
}

// DefaultRegistry is the process-wide Registry `forge steer` resolves loop
// identifiers through. See the Registry doc comment: internal/engine
// registers a running execute loop's Queue here under its Execution ID.
var DefaultRegistry = NewRegistry()

// Register attaches queue and session under loopID. A later Steer call for
// that loopID enqueues onto queue; a later Status call reports session's
// current status. session may be nil when the registering Engine has no
// executeloop.Session wired, in which case Status reports
// ErrStatusUnavailable for loopID. Register is reference-counted: calling it
// more than once for the same loopID (concurrent Workers sharing one
// Execution's loop-id) requires a matching number of Unregister calls before
// loopID is removed.
func (r *Registry) Register(loopID string, queue *Queue, session *executeloop.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reg, ok := r.loops[loopID]; ok {
		reg.count++
		return
	}
	r.loops[loopID] = &registration{queue: queue, session: session, count: 1}
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
	return r.enqueue(loopID, Message{Text: text})
}

// Answer resolves loopID to its registered Queue and enqueues text as a
// NEEDS_INFO answer. It returns ErrLoopNotFound if no loop is registered.
// Answer returns after Queue.Enqueue and does not wait for step completion.
// The loop processes the answer at its next step boundary.
func (r *Registry) Answer(loopID, text string) error {
	return r.enqueue(loopID, Message{Text: text, Kind: KindAnswer})
}

func (r *Registry) enqueue(loopID string, message Message) error {
	r.mu.Lock()
	reg, ok := r.loops[loopID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrLoopNotFound, loopID)
	}
	reg.queue.Enqueue(message)
	return nil
}

// Status resolves loopID to its registered executeloop.Session and returns
// its current running/needs_info status (TKT-009, constructorfleet/
// forge#739), so a caller can detect a paused loop without inferring it from
// transcript inactivity. It returns ErrLoopNotFound if no loop is registered
// under loopID, and ErrStatusUnavailable if loopID is registered but its
// Register call carried no Session.
func (r *Registry) Status(loopID string) (executeloop.Status, error) {
	r.mu.Lock()
	reg, ok := r.loops[loopID]
	r.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrLoopNotFound, loopID)
	}
	if reg.session == nil {
		return "", fmt.Errorf("%w: %s", ErrStatusUnavailable, loopID)
	}
	return reg.session.Status(), nil
}
