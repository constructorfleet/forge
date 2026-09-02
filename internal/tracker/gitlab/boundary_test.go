package gitlab_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/tracker/gitlab"
)

// Client must satisfy the two capabilities the wiring composes it for. The
// compile-time assertions in the package prove this too; this test states it
// where a reader of the tests can see it.
func TestClient_SatisfiesTheTrackerCapabilities(t *testing.T) {
	var _ tracker.Tracker = (*gitlab.Client)(nil)
	var _ tracker.DependencyStore = (*gitlab.Client)(nil)
	var _ tracker.AuthPreflighter = (*gitlab.Client)(nil)
	var _ tracker.SCM = (*gitlab.Client)(nil)
	var _ tracker.CI = (*gitlab.Client)(nil)
	var _ tracker.ReviewGetter = (*gitlab.Client)(nil)
	var _ tracker.ReviewsGetter = (*gitlab.Client)(nil)
	var _ tracker.MergeStatusGetter = (*gitlab.Client)(nil)
	var _ tracker.PullRequestTargetBranchGetter = (*gitlab.Client)(nil)
}

// No GitLab-native shape may cross this package's boundary (see CONTEXT.md
// "Tracker Adapter"). Every value an exported method returns must be a
// standard type, a domain type, a tracker type, or a type this package
// exports on purpose (its typed errors). A new method that returns a raw
// GitLab JSON struct fails here.
func TestClient_ExportsNoGitLabNativeTypes(t *testing.T) {
	allowedPackages := map[string]bool{
		"": true, // builtin types: string, int, bool
		"github.com/Teagan42/forge/internal/domain":         true,
		"github.com/Teagan42/forge/internal/tracker":        true,
		"github.com/Teagan42/forge/internal/tracker/gitlab": true,
	}

	clientType := reflect.TypeOf((*gitlab.Client)(nil))
	for i := 0; i < clientType.NumMethod(); i++ {
		method := clientType.Method(i)
		for out := 0; out < method.Type.NumOut(); out++ {
			result := method.Type.Out(out)
			if result.Name() == "error" || result.Kind() == reflect.Interface {
				continue
			}
			pkg := packagePathOf(result)
			if !allowedPackages[pkg] {
				t.Fatalf("method %s returns %s from package %q, which is outside the allowed set",
					method.Name, result, pkg)
			}
			// A type this package exports is allowed only when it is a typed
			// error. A raw GitLab JSON shape is not.
			if pkg == "github.com/Teagan42/forge/internal/tracker/gitlab" && !strings.HasSuffix(result.Name(), "Error") {
				t.Fatalf("method %s returns the gitlab-local type %s, which is not a typed error",
					method.Name, result)
			}
		}
	}
}

// packagePathOf reports the package a type belongs to, and looks through a
// pointer or a slice to the element type.
func packagePathOf(t reflect.Type) string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t.PkgPath()
}
