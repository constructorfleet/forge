package gitea_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

func TestCreateChangeRequest_AdaptsPullRequestCreation(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/pulls":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			_, _ = w.Write([]byte(`{"number":12,"html_url":"https://gitea.example.com/acme/widgets/pulls/12"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	cr, err := c.CreateChangeRequest(context.Background(), tracker.ChangeRequestRequest{
		Base: "main", Head: "forge/x/1", Title: "Do", Body: "desc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cr.Ref.Provider != "gitea" || cr.Ref.Number != 12 {
		t.Fatalf("unexpected change request ref: %+v", cr.Ref)
	}
	if cr.URL != "https://gitea.example.com/acme/widgets/pulls/12" {
		t.Fatalf("unexpected URL: %q", cr.URL)
	}
}
