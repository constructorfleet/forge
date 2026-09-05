package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	execbackend "github.com/Teagan42/forge/internal/execution"
)

// capturingDeadlineEnv records the ctx passed to Execute so a test can
// inspect the deadline runQualityGate derived for it, then returns result
// immediately without blocking.
type capturingDeadlineEnv struct {
	result     execbackend.Result
	capturedAt chan context.Context
}

func newCapturingDeadlineEnv(result execbackend.Result) *capturingDeadlineEnv {
	return &capturingDeadlineEnv{result: result, capturedAt: make(chan context.Context, 1)}
}

func (e *capturingDeadlineEnv) Workspace() domain.Workspace { return domain.Workspace{} }

func (e *capturingDeadlineEnv) Execute(ctx context.Context, _ execbackend.Command) (execbackend.Result, error) {
	e.capturedAt <- ctx
	return e.result, nil
}

func (e *capturingDeadlineEnv) Agent() agent.Agent              { return nil }
func (e *capturingDeadlineEnv) Cleanup(_ context.Context) error { return nil }

var _ execbackend.ExecutionEnvironment = (*capturingDeadlineEnv)(nil)

// blockingUntilCanceledEnv blocks Execute until ctx is Done, then returns
// ctx's error — it stands in for a wedged Quality Gate subprocess (e.g. a
// stalled `go test ./...`) that never returns on its own.
type blockingUntilCanceledEnv struct{}

func (blockingUntilCanceledEnv) Workspace() domain.Workspace { return domain.Workspace{} }

func (blockingUntilCanceledEnv) Execute(ctx context.Context, _ execbackend.Command) (execbackend.Result, error) {
	<-ctx.Done()
	return execbackend.Result{}, ctx.Err()
}

func (blockingUntilCanceledEnv) Agent() agent.Agent              { return nil }
func (blockingUntilCanceledEnv) Cleanup(_ context.Context) error { return nil }

var _ execbackend.ExecutionEnvironment = blockingUntilCanceledEnv{}

// TestRunQualityGate_AppliesDeadlineAsMultipleOfQualityTimeout proves
// runQualityGate (constructorfleet/forge#669) derives its own deadline for
// env.Execute from Config.Quality.Timeout, strictly greater than Timeout
// itself, mirroring executeAgent's belt-and-braces deadline (#467) but with
// its own config field and multiplier rather than reusing
// agentDeadlineMultiplier.
func TestRunQualityGate_AppliesDeadlineAsMultipleOfQualityTimeout(t *testing.T) {
	const timeout = 2 * time.Minute
	eng := &Engine{
		Now:    time.Now,
		Config: config.Config{Quality: config.QualityConfig{Timeout: timeout}},
	}

	env := newCapturingDeadlineEnv(execbackend.Result{ExitCode: 0})

	before := time.Now()
	if _, err := eng.runQualityGate(context.Background(), env, config.QualityGate{Name: "test", Command: "make test"}); err != nil {
		t.Fatalf("runQualityGate: %v", err)
	}
	after := time.Now()

	var capturedCtx context.Context
	select {
	case capturedCtx = <-env.capturedAt:
	default:
		t.Fatal("Execute was never invoked")
	}

	deadline, ok := capturedCtx.Deadline()
	if !ok {
		t.Fatal("ctx passed to Execute has no deadline, want one derived from Config.Quality.Timeout")
	}

	minDeadline := before.Add(timeout)
	if !deadline.After(minDeadline) {
		t.Errorf("deadline = %v, want strictly after started+Timeout (%v)", deadline, minDeadline)
	}

	maxDeadline := after.Add(10 * timeout)
	if deadline.After(maxDeadline) {
		t.Errorf("deadline = %v, want within a bounded multiple of Timeout (<= %v)", deadline, maxDeadline)
	}
}

// TestRunQualityGate_DeadlineBoundsAHang proves the engine's own deadline
// cancels a Quality Gate command that hangs forever (constructorfleet/forge#669),
// the same structural guarantee #467 gave one Agent invocation.
func TestRunQualityGate_DeadlineBoundsAHang(t *testing.T) {
	const timeout = 20 * time.Millisecond
	eng := &Engine{
		Now:    time.Now,
		Config: config.Config{Quality: config.QualityConfig{Timeout: timeout}},
	}

	done := make(chan error, 1)
	go func() {
		_, err := eng.runQualityGate(context.Background(), blockingUntilCanceledEnv{}, config.QualityGate{Name: "test", Command: "make test"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runQualityGate: err = nil, want an error from the engine's own deadline firing")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("runQualityGate: err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runQualityGate never returned; engine's own deadline did not bound the hang")
	}
}
