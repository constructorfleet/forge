// Package executeloop holds the state model for a running execute loop,
// separate from the loop's transcript content.
package executeloop

import "sync"

// Status is the run state of an execute loop.
type Status string

const (
	// StatusRunning means the loop executes steps normally.
	StatusRunning Status = "running"
	// StatusNeedsInfo means the loop paused and waits for user input.
	StatusNeedsInfo Status = "needs_info"
)

// Session holds the state of one execute loop run.
type Session struct {
	mu     sync.RWMutex
	status Status
}

// NewSession creates a loop session with status set to running.
func NewSession() *Session {
	return &Session{status: StatusRunning}
}

// Status returns the loop's current status. It does not parse transcript
// content.
func (s *Session) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.status
}

// SetStatus sets the loop's current status.
func (s *Session) SetStatus(status Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = status
}
