package gitea_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGetDependencies_ReadsBodyBlock(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":42,"body":"desc\n\n## Dependencies\n- #1\n- #3\n"}`))
	})

	edges, err := c.GetDependencies(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("got %d edges, want 2: %+v", len(edges), edges)
	}
	if edges[0].Issue.Provider != "gitea" || edges[0].Issue.ID != "42" {
		t.Fatalf("unexpected issue ref: %+v", edges[0].Issue)
	}
	if edges[0].DependsOn.ID != "1" || edges[1].DependsOn.ID != "3" {
		t.Fatalf("unexpected depends-on IDs: %+v", edges)
	}
}

func TestWriteDependencies_RewritesBodyBlock(t *testing.T) {
	var patched map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"number":42,"body":"desc\n\n## Dependencies: None\n"}`))
		case http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&patched); err != nil {
				t.Fatalf("decode patch body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})

	if err := c.WriteDependencies(context.Background(), "42", []string{"7", "8"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := patched["body"]
	if !strings.Contains(body, "## Dependencies") || !strings.Contains(body, "#7") || !strings.Contains(body, "#8") {
		t.Fatalf("rewritten body missing the new dependencies: %q", body)
	}
	if !strings.Contains(body, "desc") {
		t.Fatalf("rewritten body dropped the original prose: %q", body)
	}
}
