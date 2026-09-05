package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
)

// TestExecute_WorkerHeartbeatKeepsAdvancingDuringSlowPush proves a
// long-but-healthy Publisher.Push (constructorfleet/forge#463) does not
// masquerade as a wedged Agent: last_heartbeat must keep advancing for the
// whole push, even though a push has no transcript output of its own to
// touch WorkerActivity with directly, mirroring the Quality Gate keep-alive.
func TestExecute_WorkerHeartbeatKeepsAdvancingDuringSlowPush(t *testing.T) {
	te := approvedTestEngine(t, "41", domain.Issue{ID: "41", Title: "Add widget support"})
	const blockFor = 300 * time.Millisecond
	pub := &fakePublisher{commitSHA: "abc123", pushBlock: blockFor}
	te.eng.Publisher = pub
	te.eng.PRTracker = newFakePRTracker()
	te.eng.BaseBranch = "main"
	te.eng.NewExecutionID = func() string { return "exec-push-slow" }
	te.eng.HeartbeatInterval = 10 * time.Millisecond
	te.eng.HeartbeatStallAfter = 50 * time.Millisecond

	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		_, err := te.eng.Execute(ctx, "41", te.base)
		done <- err
	}()

	// Sample last_heartbeat repeatedly across the push's run: it must never
	// go longer than HeartbeatStallAfter without advancing.
	deadline := time.Now().Add(blockFor)
	var last time.Time
	var lastAdvancedAt time.Time
	for time.Now().Before(deadline) {
		claim, err := te.store.WorkerClaim(ctx, "exec-push-slow", "41")
		if err == nil {
			now := time.Now()
			if lastAdvancedAt.IsZero() {
				lastAdvancedAt = now
			}
			if !claim.LastHeartbeat.Equal(last) {
				last = claim.LastHeartbeat
				lastAdvancedAt = now
			} else if now.Sub(lastAdvancedAt) > 4*te.eng.HeartbeatStallAfter {
				t.Fatalf("last_heartbeat did not advance for %s during a healthy, slow push, want it kept advancing", now.Sub(lastAdvancedAt))
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
