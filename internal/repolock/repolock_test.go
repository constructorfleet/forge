package repolock

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWithLock_SerializesSameResource(t *testing.T) {
	t.Parallel()

	locker := New(t.TempDir())
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	order := make(chan string, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := locker.WithLock(context.Background(), "git-metadata", func() error {
			order <- "first"
			entered <- struct{}{}
			<-release
			return nil
		}); err != nil {
			t.Errorf("first WithLock: %v", err)
		}
	}()

	<-entered

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := locker.WithLock(context.Background(), "git-metadata", func() error {
			order <- "second"
			entered <- struct{}{}
			return nil
		}); err != nil {
			t.Errorf("second WithLock: %v", err)
		}
	}()

	select {
	case <-entered:
		t.Fatal("second lock holder entered before first released")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-entered
	wg.Wait()

	if got := <-order; got != "first" {
		t.Fatalf("first entry = %q, want first", got)
	}
	if got := <-order; got != "second" {
		t.Fatalf("second entry = %q, want second", got)
	}
}

func TestWithLock_AllowsIndependentResources(t *testing.T) {
	t.Parallel()

	locker := New(t.TempDir())
	release := make(chan struct{})
	entered := make(chan string, 2)
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := locker.WithLock(context.Background(), "git-metadata", func() error {
			entered <- "git"
			<-release
			return nil
		}); err != nil {
			t.Errorf("git-metadata WithLock: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := locker.WithLock(context.Background(), "branch:forge/exec/42", func() error {
			entered <- "branch"
			<-release
			return nil
		}); err != nil {
			t.Errorf("branch WithLock: %v", err)
		}
	}()

	got := map[string]bool{
		<-entered: true,
		<-entered: true,
	}
	close(release)
	wg.Wait()

	if !got["git"] || !got["branch"] {
		t.Fatalf("entered = %+v, want both independent resources", got)
	}
}
