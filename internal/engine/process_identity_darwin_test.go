//go:build darwin

package engine

import (
	"context"
	"os"
	"strings"
	"testing"
)

// darwinStartToken must carry microsecond precision, because ps -o lstart=
// only has one-second granularity and can collide on a reused pid within the
// same second. See issue 561.
func TestDarwinStartToken_HasMicrosecondPrecision(t *testing.T) {
	token, err := darwinStartToken(os.Getpid())
	if err != nil {
		t.Fatalf("darwinStartToken: %v", err)
	}
	if token == "" {
		t.Fatal("darwinStartToken returned an empty token for this process")
	}
	if !strings.Contains(token, ".") {
		t.Fatalf("token %q has no sub-second component", token)
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts[1]) == 0 || strings.TrimRight(parts[1], "0123456789") != "" {
		t.Fatalf("token %q sub-second component is not numeric", token)
	}
}

func TestDarwinStartToken_IsStableAcrossReads(t *testing.T) {
	first, err := darwinStartToken(os.Getpid())
	if err != nil {
		t.Fatalf("darwinStartToken: %v", err)
	}
	second, err := darwinStartToken(os.Getpid())
	if err != nil {
		t.Fatalf("darwinStartToken: %v", err)
	}
	if first != second {
		t.Fatalf("token changed between reads: %q then %q", first, second)
	}
}

func TestDarwinStartToken_NonPositivePIDFails(t *testing.T) {
	if _, err := darwinStartToken(0); err == nil {
		t.Fatal("darwinStartToken(0) succeeded, want an error")
	}
}

// processStartToken must prefer the microsecond-precision sysctl lookup over
// the one-second-granularity ps fallback on darwin. See issue 561.
func TestProcessStartToken_UsesMicrosecondPrecisionOnDarwin(t *testing.T) {
	ctx := context.Background()
	token := processStartToken(ctx, os.Getpid())
	if token == "" {
		t.Fatal("processStartToken returned an empty token for this process")
	}
	want, err := darwinStartToken(os.Getpid())
	if err != nil {
		t.Fatalf("darwinStartToken: %v", err)
	}
	if token != want {
		t.Fatalf("processStartToken = %q, want the sysctl token %q", token, want)
	}
}
