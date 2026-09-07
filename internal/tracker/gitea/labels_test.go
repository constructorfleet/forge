package gitea_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

// labelListHandler answers the repo label list with two labels, so a test can
// exercise the name-to-ID resolution the Gitea label endpoints require.
func labelListHandler(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`[{"id":10,"name":"needs-info"},{"id":20,"name":"in-progress"}]`))
}

func TestAddLabel_ResolvesNameToIDAndPosts(t *testing.T) {
	var postPath string
	var gotBody map[string][]int64
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/labels":
			labelListHandler(w)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/labels"):
			postPath = r.URL.Path
			_ = decodeJSON(t, r, &gotBody)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	if err := c.AddLabel(context.Background(), "5", "needs-info"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if postPath != "/repos/acme/widgets/issues/5/labels" {
		t.Fatalf("unexpected post path: %s", postPath)
	}
	if got := gotBody["labels"]; len(got) != 1 || got[0] != 10 {
		t.Fatalf("expected the resolved label ID 10 to be posted, got %v", got)
	}
}

// TestAddLabel_NonNumericIDReportsInvalidIssueID proves a non-numeric id (a
// local Feature slug) is reported as tracker.ErrInvalidIssueID, so a caller
// can tell "not a tracker issue" apart from a transient tracker error.
func TestAddLabel_NonNumericIDReportsInvalidIssueID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("must not reach the network for an invalid id: %s %s", r.Method, r.URL.Path)
	})

	err := c.AddLabel(context.Background(), "autoapply", "needs-info")
	if err == nil {
		t.Fatal("expected an error for a non-numeric issue id")
	}
	if !errors.Is(err, tracker.ErrInvalidIssueID) {
		t.Fatalf("error = %v, want it to wrap tracker.ErrInvalidIssueID", err)
	}
}

func TestAddLabel_UnknownLabelIsError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/labels" {
			labelListHandler(w)
			return
		}
		t.Fatalf("should not post an unresolved label: %s %s", r.Method, r.URL.Path)
	})

	if err := c.AddLabel(context.Background(), "5", "nonexistent"); err == nil {
		t.Fatal("expected an error adding a label the repository does not define")
	}
}

func TestRemoveLabel_ResolvesNameAndDeletesByID(t *testing.T) {
	var deletePath string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/labels":
			labelListHandler(w)
		case r.Method == http.MethodDelete:
			deletePath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	if err := c.RemoveLabel(context.Background(), "5", "in-progress"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deletePath != "/repos/acme/widgets/issues/5/labels/20" {
		t.Fatalf("unexpected delete path: %s", deletePath)
	}
}

func TestRemoveLabel_UnknownLabelIsIdempotent(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/labels" {
			labelListHandler(w)
			return
		}
		t.Fatalf("should not delete an unresolved label: %s %s", r.Method, r.URL.Path)
	})

	if err := c.RemoveLabel(context.Background(), "5", "nonexistent"); err != nil {
		t.Fatalf("expected no error removing an undefined label, got: %v", err)
	}
}

func TestRemoveLabel_DeleteNotFoundIsIdempotent(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/labels":
			labelListHandler(w)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	if err := c.RemoveLabel(context.Background(), "5", "needs-info"); err != nil {
		t.Fatalf("expected no error when the label is already absent, got: %v", err)
	}
}
