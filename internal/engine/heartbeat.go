package engine

import (
	"context"
	"time"

	"github.com/Teagan42/forge/internal/storage"
)

// heartbeatInterval is how often a live Worker's last_heartbeat advances.
const heartbeatInterval = 5 * time.Second

// RunWorkerHeartbeat beats Store.HeartbeatWorker every interval while ctx is
// live, stamping the Worker claim with now(). Display-only: no state machine
// or loss-detection logic reads the stamp. It blocks until ctx is done and
// stops silently if the claim is gone (ErrNotFound).
func RunWorkerHeartbeat(ctx context.Context, store storage.Store, executionID, issueID string, interval time.Duration, now func() time.Time) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.HeartbeatWorker(ctx, executionID, issueID, now()); err != nil {
				return
			}
		}
	}
}
