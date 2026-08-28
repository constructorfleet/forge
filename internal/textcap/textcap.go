// Package textcap provides a single, shared tail-preserving output cap for
// any package that captures a subprocess's stdout/stderr and must not let
// a runaway process force Forge to hold arbitrarily large output in
// memory. internal/gate (Quality Gate output) and internal/agent/claude
// (the Claude Code CLI's output) both use TailWriter, so the bounding
// behavior — and its test coverage — lives in exactly one place.
package textcap

// TailWriter is an io.Writer that retains at most limit bytes of
// everything written to it, keeping the TAIL — not the head — once the
// limit is exceeded. limit <= 0 means unbounded: everything written is
// retained.
//
// The tail is what matters for both current callers: Claude Code is
// instructed to emit its structured result block as the LAST thing it
// prints (so the tail preserves what parsing needs even when earlier
// output is dropped), and a failing Quality Gate's most useful diagnostic
// output is almost always near the end of the log.
type TailWriter struct {
	limit     int
	buf       []byte
	truncated bool
}

// NewTailWriter returns a TailWriter that retains at most limit bytes, or
// is unbounded if limit <= 0.
func NewTailWriter(limit int) *TailWriter {
	return &TailWriter{limit: limit}
}

// Write always reports success for the full input (matching io.Writer's
// contract that a nil error means all of p was consumed), even though
// bytes beyond limit are dropped from the head of what's retained.
func (w *TailWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	if w.limit > 0 && len(w.buf) > w.limit {
		w.truncated = true
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.limit:]...)
	}
	return n, nil
}

// String returns everything currently retained, prefixed with a marker if
// earlier output was dropped to stay within limit.
func (w *TailWriter) String() string {
	if w.truncated {
		return "... (head truncated, showing tail)\n" + string(w.buf)
	}
	return string(w.buf)
}
