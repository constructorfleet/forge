package repolock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const retryInterval = 10 * time.Millisecond

// Locker serializes named repository-scoped resources across goroutines and
// processes by taking advisory file locks under repoRoot/.forge/locks.
type Locker struct {
	root string
}

// New returns a Locker rooted under repoRoot/.forge/locks.
func New(repoRoot string) *Locker {
	return &Locker{root: filepath.Join(repoRoot, ".forge", "locks")}
}

// WithLock acquires the named resource lock, runs fn, and releases the lock.
func (l *Locker) WithLock(ctx context.Context, resource string, fn func() error) error {
	if l == nil {
		return fn()
	}
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return fmt.Errorf("repolock: create lock dir %s: %w", l.root, err)
	}

	path := filepath.Join(l.root, lockFileName(resource))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("repolock: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		} else if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("repolock: lock %s: %w", resource, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("repolock: lock %s: %w", resource, ctx.Err())
		case <-time.After(retryInterval):
		}
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	return fn()
}

func lockFileName(resource string) string {
	sum := sha256.Sum256([]byte(resource))
	return hex.EncodeToString(sum[:]) + ".lock"
}
