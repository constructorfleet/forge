package gitlab_test

import (
	"context"
	"net/http"
	"testing"
)

func TestCapabilities_PlanningMirrorIsFalse(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Capabilities must not make a network request")
	})

	if c.Capabilities().PlanningMirror {
		t.Fatal("expected PlanningMirror to be false")
	}
}

func TestCapabilities_NativeDependencyLinksIsFalseBeforeTheProbe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Capabilities must not make a network request")
	})

	if c.Capabilities().NativeDependencyLinks {
		t.Fatal("expected NativeDependencyLinks to be false before the first probe")
	}
}

func TestCapabilities_NativeDependencyLinksIsTrueAfterASuccessfulProbe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			_, _ = w.Write([]byte(`[{"iid":7,"project_id":4,"link_type":"is_blocked_by"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":""}`))
	})

	if _, err := c.GetIssue(context.Background(), "42"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !c.Capabilities().NativeDependencyLinks {
		t.Fatal("expected NativeDependencyLinks to be true after the links endpoint answered")
	}
}

func TestCapabilities_NativeDependencyLinksStaysFalseWhenTheTierHidesLinks(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isLinksPath(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"iid":42,"project_id":4,"description":"## Dependencies: None"}`))
	})

	if _, err := c.GetIssue(context.Background(), "42"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if c.Capabilities().NativeDependencyLinks {
		t.Fatal("expected NativeDependencyLinks to stay false when the tier hides the links endpoint")
	}
}
