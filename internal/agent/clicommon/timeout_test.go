package clicommon

import (
	"context"
	"testing"
	"time"
)

func TestIdleTimeout_FiresWithoutTouch(t *testing.T) {
	ctx, timedOut, _, stop := IdleTimeout(context.Background(), 20*time.Millisecond)
	defer stop()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle context was never canceled")
	}
	if !timedOut() {
		t.Fatal("timedOut() = false, want true after the deadline fired with no touch")
	}
}

func TestIdleTimeout_TouchResetsDeadline(t *testing.T) {
	ctx, timedOut, touch, stop := IdleTimeout(context.Background(), 40*time.Millisecond)
	defer stop()

	end := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(end) {
		touch()
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-ctx.Done():
		t.Fatal("idle context was canceled despite continuous touches")
	default:
	}
	if timedOut() {
		t.Fatal("timedOut() = true, want false: touches should have kept resetting the deadline")
	}
}

func TestIdleTimeout_ParentCancellationIsNotReportedAsTimeout(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, timedOut, _, stop := IdleTimeout(parent, time.Hour)
	defer stop()

	cancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle context was not canceled when its parent was canceled")
	}
	if timedOut() {
		t.Fatal("timedOut() = true, want false: the parent was canceled, not the idle deadline")
	}
}

func TestIdleTimeout_ZeroDisablesTimeout(t *testing.T) {
	parent := context.Background()
	ctx, timedOut, touch, stop := IdleTimeout(parent, 0)
	defer stop()
	touch()

	if ctx != parent {
		t.Fatal("IdleTimeout with d<=0 should return the parent context unchanged")
	}
	if timedOut() {
		t.Fatal("timedOut() = true, want false when disabled")
	}
}
