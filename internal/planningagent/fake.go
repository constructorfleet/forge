package planningagent

import (
	"context"
	"sync"

	"github.com/Teagan42/forge/internal/fake"
)

var _ Backend = (*FakeBackend)(nil)

// FakeBackend is a deterministic Backend for tests: raw outputs are
// programmed per key via the shared fake.OutcomeQueue (consume in order,
// repeat the last one, fall back to a default -- see its doc comment), so a
// planning contract's tests can script exactly what "the LLM said" without
// invoking a real backend. FakeBackend itself only owns recording
// invocations for later assertion.
type FakeBackend struct {
	outcomes *fake.OutcomeQueue[string]

	mu          sync.Mutex
	invocations []Invocation
}

// Invocation is one recorded FakeBackend.Invoke call.
type Invocation struct {
	Key    string
	Prompt string
	Schema []byte
}

// NewFakeBackend returns an empty FakeBackend with no programmed outcomes.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{outcomes: fake.NewOutcomeQueue[string]()}
}

// ProgramResult queues raw as the next raw output Invoke returns for key.
func (f *FakeBackend) ProgramResult(key, raw string) {
	f.outcomes.ProgramResult(key, raw)
}

// ProgramError queues err as the next outcome Invoke returns for key.
func (f *FakeBackend) ProgramError(key string, err error) {
	f.outcomes.ProgramError(key, err)
}

// ProgramDefault sets the raw output Invoke returns for any key with no (or
// exhausted) queued outcomes of its own.
func (f *FakeBackend) ProgramDefault(raw string) {
	f.outcomes.ProgramDefault(raw)
}

// Invoke records the call and returns the next programmed outcome for
// req.Key, per fake.OutcomeQueue's consume/repeat/default rules.
func (f *FakeBackend) Invoke(_ context.Context, req InvokeRequest) (string, error) {
	f.mu.Lock()
	f.invocations = append(f.invocations, Invocation(req))
	f.mu.Unlock()

	return f.outcomes.Next(req.Key)
}

// Invocations returns every call passed to Invoke so far, in call order.
func (f *FakeBackend) Invocations() []Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Invocation, len(f.invocations))
	copy(out, f.invocations)
	return out
}
