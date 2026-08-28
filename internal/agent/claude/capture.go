package claude

// tailLimitWriter is an io.Writer that retains at most limit bytes of
// everything written to it, keeping the TAIL — not the head — once the
// limit is exceeded. defaultRunner uses one for each of stdout and stderr
// so a runaway `claude` invocation cannot force Forge to hold arbitrarily
// large output in memory. The tail is what matters: Claude Code is
// instructed (resultContract in prompt.go) to emit its structured result
// block as the LAST thing it prints, so keeping the tail preserves the
// part parseStructuredResult needs even when earlier output is dropped.
type tailLimitWriter struct {
	limit     int
	buf       []byte
	truncated bool
}

// newTailLimitWriter returns a tailLimitWriter that retains at most limit
// bytes.
func newTailLimitWriter(limit int) *tailLimitWriter {
	return &tailLimitWriter{limit: limit}
}

// Write always reports success for the full input (matching io.Writer's
// contract that a nil error means all of p was consumed), even though
// bytes beyond limit are dropped from the head of what's retained.
func (w *tailLimitWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.truncated = true
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.limit:]...)
	}
	return n, nil
}

// String returns everything currently retained, prefixed with a marker if
// earlier output was dropped to stay within limit.
func (w *tailLimitWriter) String() string {
	if w.truncated {
		return "... (head truncated, showing tail)\n" + string(w.buf)
	}
	return string(w.buf)
}
