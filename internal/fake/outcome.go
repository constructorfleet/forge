// Package fake holds test-double machinery shared across Forge's fake
// backend implementations (agent.FakeAgent, review.FakeReviewer, and any
// future ones): the "program outcomes per key, consume in order, repeat the
// last one, fall back to a default" queue those doubles all need. Single-
// sourcing it here means the repeat-last-outcome invariant — which repair-
// iteration tests specifically rely on — is defined and tested in exactly
// one place instead of hand-copied per fake.
package fake

import (
	"fmt"
	"sync"
)

// outcome is one programmed response: either a value or an error.
type outcome[V any] struct {
	value V
	err   error
}

// OutcomeQueue is a deterministic, per-key outcome queue. Each Next(key)
// call consumes the next queued outcome for that key (or repeats the last
// one once the queue is down to a single entry, so repair-iteration tests
// can call Next more times than were explicitly programmed) and falls back
// to a shared default when nothing is programmed for the key at all. It is
// safe for concurrent use.
type OutcomeQueue[V any] struct {
	mu     sync.Mutex
	queues map[string][]outcome[V]
	deflt  *outcome[V]
}

// NewOutcomeQueue returns an empty OutcomeQueue.
func NewOutcomeQueue[V any]() *OutcomeQueue[V] {
	return &OutcomeQueue[V]{queues: map[string][]outcome[V]{}}
}

// ProgramResult queues value as the next outcome Next returns for key.
func (q *OutcomeQueue[V]) ProgramResult(key string, value V) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queues[key] = append(q.queues[key], outcome[V]{value: value})
}

// ProgramError queues err as the next outcome Next returns for key.
func (q *OutcomeQueue[V]) ProgramError(key string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queues[key] = append(q.queues[key], outcome[V]{err: err})
}

// ProgramDefault sets the outcome Next returns for any key with no (or
// exhausted) queued outcomes of its own.
func (q *OutcomeQueue[V]) ProgramDefault(value V) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deflt = &outcome[V]{value: value}
}

// Next consumes and returns the next programmed outcome for key. If key's
// queue has more than one outcome, each call consumes one until only the
// last remains, which is then repeated. If nothing is programmed for key,
// the default outcome is used; if no default is set either, Next returns
// an error.
func (q *OutcomeQueue[V]) Next(key string) (V, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	queue := q.queues[key]
	var oc outcome[V]
	switch {
	case len(queue) > 0:
		oc = queue[0]
		if len(queue) > 1 {
			q.queues[key] = queue[1:]
		}
	case q.deflt != nil:
		oc = *q.deflt
	default:
		var zero V
		return zero, fmt.Errorf("fake: no outcome programmed for key %q", key)
	}
	return oc.value, oc.err
}
