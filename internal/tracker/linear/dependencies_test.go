package linear

import (
	"context"
	"testing"
)

func TestGetDependenciesReadsBlocksRelationsOnly(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"issue(id": `{"data":{"issue":{"id":"uuid-1","identifier":"FOR-1","title":"T","description":"","url":"",
			"state":{"type":"started"},
			"inverseRelations":{"nodes":[
				{"type":"blocks","issue":{"identifier":"FOR-0"}},
				{"type":"related","issue":{"identifier":"FOR-9"}}
			]}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	edges, err := c.GetDependencies(context.Background(), "FOR-1")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want exactly 1 (only the blocks-type relation)", edges)
	}
	edge := edges[0]
	if edge.Issue.ID != "FOR-1" || edge.DependsOn.ID != "FOR-0" {
		t.Fatalf("edge = %+v, want FOR-1 depends on FOR-0", edge)
	}
	if edge.Issue.Provider != "linear" || edge.DependsOn.Provider != "linear" {
		t.Fatalf("edge providers = %+v, want linear", edge)
	}
}

func TestGetDependenciesAppliesOverrides(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-1","identifier":"FOR-1"}]}}}`,
		"issue(id": `{"data":{"issue":{"id":"uuid-1","identifier":"FOR-1","title":"T","description":"","url":"",
			"state":{"type":"started"},
			"inverseRelations":{"nodes":[{"type":"blocks","issue":{"identifier":"FOR-0"}}]}}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")
	c.DependencyOverrides = map[string][]string{"FOR-1": {"FOR-7"}}

	edges, err := c.GetDependencies(context.Background(), "FOR-1")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(edges) != 1 || edges[0].DependsOn.ID != "FOR-7" {
		t.Fatalf("edges = %+v, want override to FOR-7", edges)
	}
}
