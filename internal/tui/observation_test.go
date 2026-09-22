package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/storage"
)

type observationStoreStub struct {
	state    storage.ExecutionState
	runs     []storage.LiveRun
	events   map[int64][]storage.TranscriptEvent
	reads    []struct{ run, after int64 }
	stateErr error
	runErr   error
}

func (s *observationStoreStub) LoadExecution(context.Context, string) (storage.ExecutionState, error) {
	return s.state, s.stateErr
}
func (s *observationStoreStub) LiveRuns(context.Context) ([]storage.LiveRun, error) {
	return s.runs, s.runErr
}
func (s *observationStoreStub) TranscriptEventsAfter(_ context.Context, run, after, _ int64) ([]storage.TranscriptEvent, error) {
	s.reads = append(s.reads, struct{ run, after int64 }{run, after})
	return s.events[run], nil
}

func TestPollerPassFetchesStateAndEventsAndAdvancesCursors(t *testing.T) {
	store := &observationStoreStub{
		state:  storage.ExecutionState{Execution: structExecution("exec-1")},
		runs:   []storage.LiveRun{{AgentRunID: 7, ExecutionID: "exec-1"}},
		events: map[int64][]storage.TranscriptEvent{7: {{AgentRunID: 7, Seq: 0}, {AgentRunID: 7, Seq: 1}}},
	}
	p := NewObservationPoller(store, nil)
	first, err := p.Pass(context.Background(), "exec-1")
	if err != nil || len(first.Events[7]) != 2 || first.Cursor(7) != 1 {
		t.Fatalf("first pass = %#v, err=%v", first, err)
	}
	store.events[7] = []storage.TranscriptEvent{{AgentRunID: 7, Seq: 2}}
	second, err := p.Pass(context.Background(), "exec-1")
	if err != nil || len(second.Events[7]) != 1 || second.Cursor(7) != 2 {
		t.Fatalf("second pass = %#v, err=%v", second, err)
	}
	if len(store.reads) != 2 || store.reads[1].after != 1 {
		t.Fatalf("reads = %#v, want cursor 1 on second pass", store.reads)
	}
}

func TestPollerPassContinuesAfterReadFailures(t *testing.T) {
	store := &observationStoreStub{runs: []storage.LiveRun{{AgentRunID: 1}, {AgentRunID: 2}}, events: map[int64][]storage.TranscriptEvent{2: {{AgentRunID: 2, Seq: 0}}}}
	p := NewObservationPoller(failingObservationStore{observationStoreStub: store, failRun: 1}, nil)
	got, err := p.Pass(context.Background(), "")
	if err == nil || !errors.Is(err, errObservationRead) || len(got.Events[2]) != 1 {
		t.Fatalf("pass = %#v, err=%v; expected joined failure and successful run", got, err)
	}
}

func TestPollerRunUsesInjectedSleep(t *testing.T) {
	store := &observationStoreStub{}
	p := NewObservationPoller(store, func() time.Time { return time.Unix(10, 0) })
	sleeps := 0
	p.Sleep = func(ctx context.Context, _ time.Duration) error {
		sleeps++
		if sleeps == 1 {
			return context.Canceled
		}
		return nil
	}
	if err := p.Run(context.Background(), "", time.Second, nil); !errors.Is(err, context.Canceled) || sleeps != 1 {
		t.Fatalf("Run err=%v sleeps=%d", err, sleeps)
	}
}

type failingObservationStore struct {
	*observationStoreStub
	failRun int64
}

var errObservationRead = errors.New("read failed")

func (s failingObservationStore) TranscriptEventsAfter(ctx context.Context, run, after, limit int64) ([]storage.TranscriptEvent, error) {
	if run == s.failRun {
		return nil, errObservationRead
	}
	return s.observationStoreStub.TranscriptEventsAfter(ctx, run, after, limit)
}

func structExecution(id string) (e domain.Execution) { e.ID = id; return }
