package textcap_test

import (
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/textcap"
)

func TestTailWriter_RetainsEverythingUnderLimit(t *testing.T) {
	w := textcap.NewTailWriter(100)
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if got := w.String(); got != "hello" {
		t.Errorf("String() = %q, want %q", got, "hello")
	}
}

func TestTailWriter_KeepsTailNotHeadOnceOverLimit(t *testing.T) {
	w := textcap.NewTailWriter(4)
	if _, err := w.Write([]byte("0123456789")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := w.String()
	if got[len(got)-4:] != "6789" {
		t.Errorf("String() tail = %q, want %q", got[len(got)-4:], "6789")
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("String() = %q, want it to note truncation", got)
	}
}

func TestTailWriter_MultipleWritesAccumulateAndBound(t *testing.T) {
	w := textcap.NewTailWriter(5)
	_, _ = w.Write([]byte("abc"))
	_, _ = w.Write([]byte("def"))
	got := w.String()
	if got[len(got)-5:] != "bcdef" {
		t.Errorf("String() tail = %q, want %q", got[len(got)-5:], "bcdef")
	}
}

func TestTailWriter_ZeroOrNegativeLimitIsUnbounded(t *testing.T) {
	for _, limit := range []int{0, -1} {
		w := textcap.NewTailWriter(limit)
		big := strings.Repeat("x", 10000)
		if _, err := w.Write([]byte(big)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := w.String(); got != big {
			t.Errorf("limit %d: String() len = %d, want unbounded %d", limit, len(got), len(big))
		}
	}
}

func TestTailWriter_NoTruncationMarkerWhenUnderLimit(t *testing.T) {
	w := textcap.NewTailWriter(100)
	_, _ = w.Write([]byte("short"))
	if got := w.String(); strings.Contains(got, "truncated") {
		t.Errorf("String() = %q, want no truncation marker", got)
	}
}

func TestTailWriter_WriteReportsFullLengthEvenWhenTruncated(t *testing.T) {
	w := textcap.NewTailWriter(2)
	n, err := w.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 6 {
		t.Errorf("n = %d, want 6 (io.Writer contract: nil error means all bytes consumed)", n)
	}
}
