package gitlab_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
)

// mergeRequirementsHandler answers the two endpoints GetMergeRequirements
// reads: the project settings and the approval rules. Each argument is the
// response body for one endpoint; an empty status means 200.
func mergeRequirementsHandler(t *testing.T, projectBody, approvalRulesBody string, approvalRulesStatus int) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.EscapedPath(), "/approval_rules"):
			if approvalRulesStatus != 0 {
				w.WriteHeader(approvalRulesStatus)
				return
			}
			_, _ = w.Write([]byte(approvalRulesBody))
		case r.URL.EscapedPath() == "/projects/"+escapedProject:
			_, _ = w.Write([]byte(projectBody))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
	}
}

func TestGetMergeRequirements_RequiresThePipelineWhenTheProjectDoes(t *testing.T) {
	c, _ := newTestClient(t, mergeRequirementsHandler(t,
		`{"only_allow_merge_if_pipeline_succeeds":true}`, `[]`, 0))

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("GetMergeRequirements: %v", err)
	}
	wantReqs := []tracker.MergeRequirement{{CheckName: "pipeline"}}
	if !reflect.DeepEqual(reqs.Requirements, wantReqs) {
		t.Fatalf("Requirements = %+v, want %+v", reqs.Requirements, wantReqs)
	}
	if !reflect.DeepEqual(reqs.RequiredChecks, []string{"pipeline"}) {
		t.Fatalf("RequiredChecks = %+v, want [pipeline]", reqs.RequiredChecks)
	}
}

func TestGetMergeRequirements_AddsApprovalsButNotAsACheck(t *testing.T) {
	c, _ := newTestClient(t, mergeRequirementsHandler(t,
		`{"only_allow_merge_if_pipeline_succeeds":true}`,
		`[{"name":"Default","approvals_required":2}]`, 0))

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("GetMergeRequirements: %v", err)
	}
	wantReqs := []tracker.MergeRequirement{{CheckName: "pipeline"}, {CheckName: "approvals"}}
	if !reflect.DeepEqual(reqs.Requirements, wantReqs) {
		t.Fatalf("Requirements = %+v, want %+v", reqs.Requirements, wantReqs)
	}
	// RequiredChecks is compared against the reported checks. Approvals are
	// not a check, so they must stay out of it.
	if !reflect.DeepEqual(reqs.RequiredChecks, []string{"pipeline"}) {
		t.Fatalf("RequiredChecks = %+v, want [pipeline]", reqs.RequiredChecks)
	}
}

func TestGetMergeRequirements_IgnoresAnApprovalRuleThatRequiresNoApproval(t *testing.T) {
	c, _ := newTestClient(t, mergeRequirementsHandler(t,
		`{"only_allow_merge_if_pipeline_succeeds":false}`,
		`[{"name":"Default","approvals_required":0}]`, 0))

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("GetMergeRequirements: %v", err)
	}
	if len(reqs.Requirements) != 0 || len(reqs.RequiredChecks) != 0 {
		t.Fatalf("requirements = %+v, want none", reqs)
	}
}

func TestGetMergeRequirements_ReportsNoneWhenTheBranchIsUngated(t *testing.T) {
	c, _ := newTestClient(t, mergeRequirementsHandler(t,
		`{"only_allow_merge_if_pipeline_succeeds":false}`, `[]`, 0))

	reqs, err := c.GetMergeRequirements(context.Background(), "main")
	if err != nil {
		t.Fatalf("GetMergeRequirements: %v", err)
	}
	if !reflect.DeepEqual(reqs, tracker.MergeRequirements{}) {
		t.Fatalf("requirements = %+v, want the zero value", reqs)
	}
}

// A project tier without approval rules answers 403 or 404. That means "this
// project has no approval requirement", not "the request failed".
func TestGetMergeRequirements_DegradesWhenTheTierHidesApprovalRules(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, _ := newTestClient(t, mergeRequirementsHandler(t,
				`{"only_allow_merge_if_pipeline_succeeds":true}`, "", status))

			reqs, err := c.GetMergeRequirements(context.Background(), "main")
			if err != nil {
				t.Fatalf("GetMergeRequirements: %v", err)
			}
			wantReqs := []tracker.MergeRequirement{{CheckName: "pipeline"}}
			if !reflect.DeepEqual(reqs.Requirements, wantReqs) {
				t.Fatalf("Requirements = %+v, want %+v", reqs.Requirements, wantReqs)
			}
		})
	}
}

// A failure the adapter cannot interpret must stay a failure.
func TestGetMergeRequirements_ReportsAProjectReadFailure(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})

	if _, err := c.GetMergeRequirements(context.Background(), "main"); err == nil {
		t.Fatal("expected an error, got nil")
	}
}
