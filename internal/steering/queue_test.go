package steering_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/steering"
)

func TestQueue_DrainReturnsMessagesInEnqueueOrder(t *testing.T) {
	q := steering.NewQueue()

	q.Enqueue(steering.Message{Text: "first"})
	q.Enqueue(steering.Message{Text: "second"})
	q.Enqueue(steering.Message{Text: "third"})

	got := q.Drain()

	want := []steering.Message{{Text: "first"}, {Text: "second"}, {Text: "third"}}
	if len(got) != len(want) {
		t.Fatalf("Drain() returned %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestQueue_DrainPreservesKind proves a Message's Kind survives Enqueue and
// Drain unchanged, so a caller can tell a free-form steering instruction
// (KindSteering, also the zero value) from a NEEDS_INFO answer (KindAnswer)
// after draining (constructorfleet/forge#745).
func TestQueue_DrainPreservesKind(t *testing.T) {
	q := steering.NewQueue()

	q.Enqueue(steering.Message{Text: "steer this way"})
	q.Enqueue(steering.Message{Text: "42", Kind: steering.KindAnswer})

	got := q.Drain()

	want := []steering.Message{
		{Text: "steer this way", Kind: steering.KindSteering},
		{Text: "42", Kind: steering.KindAnswer},
	}
	if len(got) != len(want) {
		t.Fatalf("Drain() returned %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestQueue_DrainEmptiesTheQueue(t *testing.T) {
	q := steering.NewQueue()
	q.Enqueue(steering.Message{Text: "only"})

	_ = q.Drain()
	got := q.Drain()

	if len(got) != 0 {
		t.Fatalf("Drain() after a prior Drain() = %+v, want empty", got)
	}
}

func TestQueue_DrainAllConcatenatesMessagesInFIFOOrder(t *testing.T) {
	q := steering.NewQueue()

	q.Enqueue(steering.Message{Text: "from user"})
	q.Enqueue(steering.Message{Text: "from needs-info answer"})
	q.Enqueue(steering.Message{Text: "from another source"})

	got := q.DrainAll()

	want := "from user\nfrom needs-info answer\nfrom another source"
	if got != want {
		t.Fatalf("DrainAll() = %q, want %q", got, want)
	}
}

func TestQueue_DrainAllEmptiesTheQueue(t *testing.T) {
	q := steering.NewQueue()
	q.Enqueue(steering.Message{Text: "only"})

	_ = q.DrainAll()
	got := q.DrainAll()

	if got != "" {
		t.Fatalf("DrainAll() after a prior DrainAll() = %q, want empty", got)
	}
}

func TestQueue_DrainAllOnEmptyQueueReturnsEmptyString(t *testing.T) {
	q := steering.NewQueue()

	got := q.DrainAll()

	if got != "" {
		t.Fatalf("DrainAll() on empty queue = %q, want empty", got)
	}
}

func TestQueue_ConcurrentEnqueueDuringSimulatedStep_PreservesOrderAndLosesNothing(t *testing.T) {
	q := steering.NewQueue()

	// Simulate a step-execution goroutine that is busy for the duration of
	// the test: it never touches the queue, so Enqueue must not block on it.
	stepDone := make(chan struct{})
	stepStarted := make(chan struct{})
	go func() {
		close(stepStarted)
		<-stepDone
	}()
	<-stepStarted

	const producers = 8
	const perProducer = 50
	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(producer int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				q.Enqueue(steering.Message{Text: seqText(producer, i)})
			}
		}(p)
	}
	wg.Wait()
	close(stepDone)

	got := q.Drain()
	if len(got) != producers*perProducer {
		t.Fatalf("Drain() returned %d messages, want %d", len(got), producers*perProducer)
	}

	// Enqueue order is undefined across producers (they race each other),
	// but each producer's own messages must survive in the order it
	// enqueued them relative to its own sequence.
	lastSeen := make(map[int]int)
	for p := 0; p < producers; p++ {
		lastSeen[p] = -1
	}
	for _, msg := range got {
		producer, seq := parseSeqText(t, msg.Text)
		if seq <= lastSeen[producer] {
			t.Fatalf("producer %d: message seq %d arrived after seq %d, out of order", producer, seq, lastSeen[producer])
		}
		lastSeen[producer] = seq
	}
	for p := 0; p < producers; p++ {
		if lastSeen[p] != perProducer-1 {
			t.Fatalf("producer %d: last seen seq %d, want %d (messages lost)", p, lastSeen[p], perProducer-1)
		}
	}
}

// TestQueue_WaitReturnsImmediatelyWhenMessagesAlreadyQueued proves Wait does
// not block a caller that reaches it after a message was already enqueued
// (e.g. a NEEDS_INFO answer that arrived while the paused loop's caller was
// still setting up the wait) — TKT-006's resume path must not miss a
// message that raced ahead of it.
func TestQueue_WaitReturnsImmediatelyWhenMessagesAlreadyQueued(t *testing.T) {
	q := steering.NewQueue()
	q.Enqueue(steering.Message{Text: "already here"})

	done := make(chan struct{})
	go func() {
		q.Wait(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait() blocked despite a message already queued")
	}
}

// TestQueue_WaitBlocksUntilEnqueue proves Wait blocks a caller with an empty
// queue until Enqueue is called from another goroutine — the "loop waits,
// does not poll" acceptance criterion.
func TestQueue_WaitBlocksUntilEnqueue(t *testing.T) {
	q := steering.NewQueue()

	done := make(chan struct{})
	go func() {
		q.Wait(context.Background())
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Wait() returned before Enqueue was ever called")
	case <-time.After(50 * time.Millisecond):
	}

	q.Enqueue(steering.Message{Text: "the answer"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait() did not return after Enqueue")
	}
}

// TestQueue_WaitReturnsWhenContextDone proves Wait does not hang forever
// when its ctx is cancelled before any message ever arrives.
func TestQueue_WaitReturnsWhenContextDone(t *testing.T) {
	q := steering.NewQueue()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		q.Wait(ctx)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Wait() returned before ctx was cancelled or any message arrived")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait() did not return after ctx cancellation")
	}
}

func seqText(producer, seq int) string {
	return strconv.Itoa(producer) + ":" + strconv.Itoa(seq)
}

func parseSeqText(t *testing.T, text string) (producer, seq int) {
	t.Helper()
	sep := strings.IndexByte(text, ':')
	if sep < 0 {
		t.Fatalf("parseSeqText: no separator in %q", text)
	}
	p, err := strconv.Atoi(text[:sep])
	if err != nil {
		t.Fatalf("parseSeqText: bad producer in %q: %v", text, err)
	}
	s, err := strconv.Atoi(text[sep+1:])
	if err != nil {
		t.Fatalf("parseSeqText: bad seq in %q: %v", text, err)
	}
	return p, s
}
