package engine

import (
	"context"
	"os"
	"testing"
)

func TestProcessStartToken_IdentifiesThisProcess(t *testing.T) {
	ctx := context.Background()
	token := processStartToken(ctx, os.Getpid())
	if token == "" {
		t.Fatal("processStartToken returned an empty token for this process")
	}
	if again := processStartToken(ctx, os.Getpid()); again != token {
		t.Fatalf("token changed between reads: %q then %q", token, again)
	}
}

func TestProcessStartToken_NonPositivePIDHasNoToken(t *testing.T) {
	if token := processStartToken(context.Background(), 0); token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
}

// ownerToken must not keep a token that a different OwnerPID produced.
func TestOwnerToken_RecomputesWhenOwnerPIDChanges(t *testing.T) {
	pid := 100
	calls := 0
	e := &Engine{
		OwnerPID: func() int { return pid },
		ProcessStartToken: func(_ context.Context, p int) string {
			calls++
			return "token-" + string(rune('0'+p%10))
		},
	}
	ctx := context.Background()
	if got := e.ownerToken(ctx); got != "token-0" {
		t.Fatalf("ownerToken = %q, want token-0", got)
	}
	if got := e.ownerToken(ctx); got != "token-0" || calls != 1 {
		t.Fatalf("ownerToken = %q after %d lookups, want a cached token-0", got, calls)
	}
	pid = 101
	if got := e.ownerToken(ctx); got != "token-1" {
		t.Fatalf("ownerToken = %q after the pid changed, want token-1", got)
	}
}

// An empty token is cached too, so a hostile lookup does not run per claim.
func TestOwnerToken_CachesAnEmptyToken(t *testing.T) {
	calls := 0
	e := &Engine{
		OwnerPID: func() int { return 7 },
		ProcessStartToken: func(context.Context, int) string {
			calls++
			return ""
		},
	}
	ctx := context.Background()
	_ = e.ownerToken(ctx)
	_ = e.ownerToken(ctx)
	if calls != 1 {
		t.Fatalf("lookups = %d, want 1", calls)
	}
}
