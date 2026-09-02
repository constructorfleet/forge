package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestAddLabel_PutsAddLabelsOnTheIssue(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"iid":42}`))
	})

	if err := c.AddLabel(context.Background(), "42", "needs-info"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/projects/"+escapedProject+"/issues/42" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody["add_labels"] != "needs-info" {
		t.Fatalf("request body = %+v, want add_labels", gotBody)
	}
	if _, ok := gotBody["remove_labels"]; ok {
		t.Fatalf("request body = %+v, want no remove_labels key", gotBody)
	}
}

func TestRemoveLabel_PutsRemoveLabelsOnTheIssue(t *testing.T) {
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"iid":42}`))
	})

	if err := c.RemoveLabel(context.Background(), "42", "needs-info"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if gotBody["remove_labels"] != "needs-info" {
		t.Fatalf("request body = %+v, want remove_labels", gotBody)
	}
	if _, ok := gotBody["add_labels"]; ok {
		t.Fatalf("request body = %+v, want no add_labels key", gotBody)
	}
}

func TestLabels_AreIdempotent(t *testing.T) {
	// GitLab's add_labels and remove_labels parameters are idempotent: a
	// label that is already set, or already absent, is not an error. Two
	// calls in a row must both succeed.
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"iid":42}`))
	})

	for i := 0; i < 2; i++ {
		if err := c.AddLabel(context.Background(), "42", "in-progress"); err != nil {
			t.Fatalf("AddLabel call %d: %v", i, err)
		}
		if err := c.RemoveLabel(context.Background(), "42", "in-progress"); err != nil {
			t.Fatalf("RemoveLabel call %d: %v", i, err)
		}
	}
	if calls != 4 {
		t.Fatalf("made %d requests, want 4", calls)
	}
}

func TestRemoveLabel_PropagatesA404AsAnError(t *testing.T) {
	// GitLab's remove_labels parameter is already idempotent for an absent
	// label: it does not 404 for that case. A 404 from this endpoint means
	// the Issue itself is not found, so RemoveLabel must report it as an
	// error, not swallow it as success.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Issue Not Found"}`))
	})

	if err := c.RemoveLabel(context.Background(), "42", "gone"); err == nil {
		t.Fatal("expected RemoveLabel to return an error on 404")
	}
}

func TestLabels_RejectALabelThatHoldsAComma(t *testing.T) {
	// GitLab reads add_labels and remove_labels as a comma-separated list,
	// so a label that holds a comma would become two labels. The adapter
	// fails loudly rather than apply something the caller did not ask for.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no HTTP request for a label that holds a comma")
	})

	if err := c.AddLabel(context.Background(), "42", "needs-info,blocked"); err == nil {
		t.Fatal("expected AddLabel to reject a label that holds a comma")
	}
	if err := c.RemoveLabel(context.Background(), "42", "needs-info,blocked"); err == nil {
		t.Fatal("expected RemoveLabel to reject a label that holds a comma")
	}
}

func TestLabels_RejectNonNumericIssueID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("expected no HTTP request for an invalid issue id")
	})

	if err := c.AddLabel(context.Background(), "nope", "x"); err == nil {
		t.Fatal("expected AddLabel to reject a non-numeric issue id")
	}
	if err := c.RemoveLabel(context.Background(), "nope", "x"); err == nil {
		t.Fatal("expected RemoveLabel to reject a non-numeric issue id")
	}
}
