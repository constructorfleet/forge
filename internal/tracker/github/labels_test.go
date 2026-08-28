package github_test

import (
	"context"
	"net/http"
	"testing"
)

func TestAddLabel_Idempotent(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.AddLabel(context.Background(), "5", "needs-info"); err != nil {
		t.Fatalf("first add: unexpected error: %v", err)
	}
	if err := c.AddLabel(context.Background(), "5", "needs-info"); err != nil {
		t.Fatalf("second add: unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestRemoveLabel_IdempotentWhenAlreadyAbsent(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Label does not exist"}`))
	})

	if err := c.RemoveLabel(context.Background(), "5", "needs-info"); err != nil {
		t.Fatalf("expected no error removing an absent label, got: %v", err)
	}
}

func TestRemoveLabel_PropagatesOtherErrors(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := c.RemoveLabel(context.Background(), "5", "needs-info"); err == nil {
		t.Fatal("expected an error to propagate")
	}
}
