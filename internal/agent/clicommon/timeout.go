package clicommon

import (
	"context"
	"sync/atomic"
	"time"
)

// IdleTimeout derives a child context from ctx that is canceled if the
// returned touch function is not called for d — a stall detector for a
// subprocess whose Runner reports each line of output it produces via
// onLine (issue 33, "Agent runs need a timeout"). Callers invoke touch on
// every sign of progress (e.g. from onLine) to reset the deadline, so a
// long-but-progressing run is never killed, while a genuinely wedged
// subprocess that stops producing output trips the timeout after d of
// silence. A caller that never invokes touch gets a plain fixed timeout of
// d from when IdleTimeout was called.
//
// d <= 0 disables the timeout entirely: the returned context is ctx itself,
// and touch/stop are no-ops.
//
// timedOut distinguishes "this deadline fired" from "ctx was canceled by
// its parent for an unrelated reason" — both cancel the returned context,
// but only the former should be reported as a timeout. stop releases the
// internal timer and goroutine; callers must call it (typically via defer)
// once the subprocess invocation is done, whether or not it timed out.
func IdleTimeout(ctx context.Context, d time.Duration) (idleCtx context.Context, timedOut func() bool, touch func(), stop func()) {
	if d <= 0 {
		return ctx, func() bool { return false }, func() {}, func() {}
	}

	idleCtx, cancel := context.WithCancel(ctx)
	touchCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	var expired atomic.Bool

	go func() {
		defer cancel()
		timer := time.NewTimer(d)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				expired.Store(true)
				return
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-touchCh:
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(d)
			}
		}
	}()

	touch = func() {
		select {
		case touchCh <- struct{}{}:
		default:
			// A touch is already pending; the timer will be reset once the
			// watcher goroutine drains it, which is equivalent to this
			// touch for the purpose of resetting the deadline.
		}
	}
	var stopped atomic.Bool
	stop = func() {
		if stopped.CompareAndSwap(false, true) {
			close(stopCh)
		}
	}
	return idleCtx, expired.Load, touch, stop
}
