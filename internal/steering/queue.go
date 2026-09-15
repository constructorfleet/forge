// Package steering holds Queue: a thread-safe FIFO queue that attaches to an
// execute loop and accepts steering messages and NEEDS_INFO answers at any
// time, including while a step is executing.
package steering

import "sync"

// Message is one steering input queued for the execute loop: a free-form
// steering instruction or a NEEDS_INFO answer from a human.
type Message struct {
	Text string
}

// Queue is a thread-safe FIFO queue of steering Messages. A caller enqueues
// messages from a goroutine separate from the step-execution goroutine;
// Enqueue never blocks on step completion.
type Queue struct {
	mu       sync.Mutex
	messages []Message
}

// NewQueue returns an empty Queue ready for concurrent use.
func NewQueue() *Queue {
	return &Queue{}
}

// Enqueue adds msg to the back of the queue. It is safe to call from any
// goroutine, at any time, regardless of whether a step is currently running.
func (q *Queue) Enqueue(msg Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, msg)
}

// Drain removes and returns all queued Messages in FIFO order, leaving the
// queue empty. It returns nil if the queue is empty.
func (q *Queue) Drain() []Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return nil
	}
	drained := q.messages
	q.messages = nil
	return drained
}
