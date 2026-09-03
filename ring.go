package main

// ring is a capped FIFO of rendered lines. Bubbles viewport has no append
// API, no line cap, and recomputes maxLineWidth over all lines per
// SetContent -- so the transcript pane owns retention itself (#445).
type ring struct {
	buf     []string
	start   int // index of oldest line
	n       int // lines held
	evicted int // lines dropped, for the "earlier events not retained" marker
}

func newRing(cap int) *ring { return &ring{buf: make([]string, cap)} }

func (r *ring) push(lines ...string) {
	for _, l := range lines {
		if r.n == len(r.buf) {
			r.start = (r.start + 1) % len(r.buf)
			r.n--
			r.evicted++
		}
		r.buf[(r.start+r.n)%len(r.buf)] = l
		r.n++
	}
}

// lines materializes the window in order. The only O(n) operation, so it
// runs once per rendered frame, never per event.
func (r *ring) lines() []string {
	out := make([]string, r.n)
	for i := 0; i < r.n; i++ {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}

func (r *ring) len() int { return r.n }
