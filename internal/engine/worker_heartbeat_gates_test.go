package engine_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
)

// slowCommandRunner simulates a healthy but slow Quality Gate command (e.g.
// a real `go test ./...`), which streams no per-line output of its own —
// unlike an Agent invocation, a Quality Gate has nothing for
// touchWorkerActivity to key off directly.
type slowCommandRunner struct {
	block time.Duration
}

func (r slowCommandRunner) Run(ctx context.Context, _, _ string, _, _ io.Writer) (int, error) {
	select {
	case <-time.After(r.block):
	case <-ctx.Done():
		return -1, ctx.Err()
	}
	return 0, nil
}

// TestExecute_WorkerHeartbeatKeepsAdvancingDuringSlowQualityGate proves a
// long-but-healthy Quality Gate (constructorfleet/forge#463) does not
// masquerade as a wedged Agent: last_heartbeat must keep advancing for the
// whole gate run, even though a gate has no transcript output of its own to
// touch WorkerActivity with directly.
func TestExecute_WorkerHeartbeatKeepsAdvancingDuringSlowQualityGate(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"20": {ID: "20"},
	})
	te.fake.ProgramResult("20", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	const blockFor = 300 * time.Millisecond
	te.gates.Set(slowCommandRunner{block: blockFor})
	te.eng.NewExecutionID = func() string { return "exec-gate-slow" }
	te.eng.HeartbeatInterval = 10 * time.Millisecond
	te.eng.HeartbeatStallAfter = 50 * time.Millisecond

	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		_, err := te.eng.Execute(ctx, "20", te.base)
		done <- err
	}()

	// Sample last_heartbeat repeatedly across the gate's run: it must never
	// go longer than HeartbeatStallAfter without advancing.
	deadline := time.Now().Add(blockFor)
	var last time.Time
	var lastAdvancedAt time.Time
	for time.Now().Before(deadline) {
		claim, err := te.store.WorkerClaim(ctx, "exec-gate-slow", "20")
		if err == nil {
			now := time.Now()
			if lastAdvancedAt.IsZero() {
				lastAdvancedAt = now
			}
			if !claim.LastHeartbeat.Equal(last) {
				last = claim.LastHeartbeat
				lastAdvancedAt = now
			} else if now.Sub(lastAdvancedAt) > 4*te.eng.HeartbeatStallAfter {
				t.Fatalf("last_heartbeat did not advance for %s during a healthy, slow Quality Gate run, want it kept advancing", now.Sub(lastAdvancedAt))
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute never returned")
	}
}
