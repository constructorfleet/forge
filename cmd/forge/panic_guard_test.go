package main

import (
	"strings"
	"testing"
)

func TestWithPanicGuardRecoversAndReturns1(t *testing.T) {
	stderr := captureStderr(t, func() {
		code := withPanicGuard("forge test", func() int { panic("boom") })
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "forge test: panic: boom") {
		t.Fatalf("stderr = %q, want a panic message naming the command and the recovered value", stderr)
	}
}

func TestWithPanicGuardPassesThroughNormalReturn(t *testing.T) {
	if code := withPanicGuard("forge test", func() int { return 2 }); code != 2 {
		t.Fatalf("code = %d, want 2 passed through untouched", code)
	}
}
