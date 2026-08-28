package gate

// boundedWriter is an io.Writer that retains at most limit bytes of
// everything written to it, keeping the TAIL — not the head — once the
// limit is exceeded. Runner uses one per captured stream (stdout, stderr)
// so a runaway gate command cannot force Forge to hold arbitrarily large
// output in memory, and so the diagnostic feedback built from a failing
// gate's output favors the most recent lines, where the actual failure
// almost always is. limit <= 0 means unbounded — everything written is
// retained.
//
// This mirrors internal/agent/claude/capture.go's tailLimitWriter; the two
// are kept separate rather than shared so internal/gate has no dependency
// on internal/agent/claude.
type boundedWriter struct {
	limit     int
	buf       []byte
	truncated bool
}

// newBoundedWriter returns a boundedWriter that retains at most limit
// bytes, or is unbounded if limit <= 0.
func newBoundedWriter(limit int) *boundedWriter {
	return &boundedWriter{limit: limit}
}

// Write always reports success for the full input (matching io.Writer's
// contract that a nil error means all of p was consumed), even though
// bytes beyond limit are dropped from the head of what's retained.
func (w *boundedWriter) Write(p []byte) (int, error) {
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
func (w *boundedWriter) String() string {
	if w.truncated {
		return "... (head truncated, showing tail)\n" + string(w.buf)
	}
	return string(w.buf)
}
