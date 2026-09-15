package steering_test

import (
	"strconv"
	"strings"
	"sync"
	"testing"

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
