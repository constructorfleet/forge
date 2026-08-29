package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/semantic"
)

// fakeSemanticSession is a semantic.Session double: Augment stamps a
// recognizable NativeServer entry so tests can assert the descriptor
// actually reached the AgentRequest, and both methods count their calls.
type fakeSemanticSession struct {
	mu        sync.Mutex
	augmented int
	torndown  int
}

func (s *fakeSemanticSession) Augment(req agent.AgentRequest) agent.AgentRequest {
	s.mu.Lock()
	s.augmented++
	s.mu.Unlock()
	req.Semantic.NativeServers = append(req.Semantic.NativeServers, agent.NativeServer{Language: "go", Command: []string{"gopls"}})
	return req
}

func (s *fakeSemanticSession) Teardown() {
	s.mu.Lock()
	s.torndown++
	s.mu.Unlock()
}

func (s *fakeSemanticSession) Counts() (augmented, torndown int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.augmented, s.torndown
}

// fakeSemanticProvider is a semantic.Provider double that hands out a fixed
// Session and counts how many times Prepare was called, so tests can assert
// "one Session per Issue, reused across the repair loop" (issue #126).
type fakeSemanticProvider struct {
	session *fakeSemanticSession

	mu       sync.Mutex
	prepared int
	servers  []semantic.DetectedServer
}

func (p *fakeSemanticProvider) Prepare(_ context.Context, _ string, _ agent.RepositoryContext, servers []semantic.DetectedServer) semantic.Session {
	p.mu.Lock()
	p.prepared++
	p.servers = servers
	p.mu.Unlock()
	return p.session
}

func (p *fakeSemanticProvider) Servers() []semantic.DetectedServer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.servers
}

func (p *fakeSemanticProvider) Prepared() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prepared
}

var _ semantic.Provider = (*fakeSemanticProvider)(nil)
var _ semantic.Session = (*fakeSemanticSession)(nil)

func TestExecute_SemanticProvider_AugmentsAgentRequestAndTearsDownOnce(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"60": {ID: "60"},
	})
	te.fake.ProgramResult("60", agent.AgentResult{Status: agent.StatusImplemented})

	sess := &fakeSemanticSession{}
	provider := &fakeSemanticProvider{session: sess}
	te.eng.Semantic = provider

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "60", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	invocations := te.fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("got %d agent invocations, want 1", len(invocations))
	}
	got := invocations[0].Semantic.NativeServers
	if len(got) != 1 || got[0].Language != "go" {
		t.Errorf("AgentRequest.Semantic.NativeServers = %+v, want the Session's stamped {go, [gopls]} entry", got)
	}

	if provider.Prepared() != 1 {
		t.Errorf("Provider.Prepare called %d times, want 1", provider.Prepared())
	}
	augmented, torndown := sess.Counts()
	if augmented != 1 {
		t.Errorf("Session.Augment called %d times, want 1", augmented)
	}
	if torndown != 1 {
		t.Errorf("Session.Teardown called %d times, want 1", torndown)
	}
}

// TestExecute_SemanticProvider_SessionReusedAcrossRepairLoop is this
// ticket's "one Session per Issue, reused across the repair loop"
// acceptance criterion: a gate failure triggers one repair iteration (a
// second Agent.Execute call within the same Issue), and Prepare must still
// only have been called once.
func TestExecute_SemanticProvider_SessionReusedAcrossRepairLoop(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"61": {ID: "61"},
	})
	te.fake.ProgramResult("61", agent.AgentResult{Status: agent.StatusImplemented})
	te.eng.Config.Quality.Gates = []config.QualityGate{{Name: "test", Command: "make test"}}
	te.eng.Config.Retry = domain.RetryLimits{Gate: 1, Review: 1, CI: 1}
	runner := &flakyRunner{failUntil: 1}
	te.eng.Gates = runner

	sess := &fakeSemanticSession{}
	provider := &fakeSemanticProvider{session: sess}
	te.eng.Semantic = provider

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "61", te.base)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING", result.Issue.State)
	}

	if invocations := te.fake.Invocations(); len(invocations) != 2 {
		t.Fatalf("got %d agent invocations, want 2 (initial attempt + one gate repair)", len(invocations))
	}
	if provider.Prepared() != 1 {
		t.Errorf("Provider.Prepare called %d times across the repair loop, want 1 (one Session per Issue)", provider.Prepared())
	}
	if augmented, torndown := sess.Counts(); augmented != 2 || torndown != 1 {
		t.Errorf("Session.Augment/Teardown counts = %d/%d, want 2/1 (augmented on both attempts, torn down once)", augmented, torndown)
	}
}

// TestExecute_SemanticProviderReturningInertSession_NeverFailsTheWorker is
// this ticket's degradation acceptance criterion: whatever a Provider does
// internally to end up with nothing to add (a missing backend profile, a
// disabled lsp config, an unwritable provisioning step — all opaque to
// Engine), Prepare's contract is that it never errors, and Engine must
// reach the same resting state as if no Provider were wired at all.
func TestExecute_SemanticProviderReturningInertSession_NeverFailsTheWorker(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"62": {ID: "62"},
	})
	te.fake.ProgramResult("62", agent.AgentResult{Status: agent.StatusImplemented})

	// A Provider that degraded internally (e.g. no declared backend
	// profile, disabled lsp config) returns a Session whose Augment is a
	// pure passthrough — see internal/semantic's own inert-Session tests.
	te.eng.Semantic = inertSemanticProvider{}

	ctx := context.Background()
	result, err := te.eng.Execute(ctx, "62", te.base)
	if err != nil {
		t.Fatalf("Execute: %v, want no error even though semantic provisioning degraded to inert", err)
	}
	if result.Issue.State != domain.StateCommitting {
		t.Fatalf("final state = %s, want COMMITTING (a degraded Session must not change the Worker's outcome)", result.Issue.State)
	}

	got := te.fake.Invocations()[0].Semantic
	if len(got.MCPServers) != 0 || len(got.NativeServers) != 0 {
		t.Errorf("Semantic descriptor = %+v, want empty (inert Session degrades to no navigation)", got)
	}
}

// inertSemanticProvider is a semantic.Provider whose Session.Augment is a
// pure passthrough — the shape a real semantic.Provider degrades to when
// it has nothing to add (see internal/semantic's own inert-Session tests).
type inertSemanticProvider struct{}

func (inertSemanticProvider) Prepare(context.Context, string, agent.RepositoryContext, []semantic.DetectedServer) semantic.Session {
	return inertSemanticSession{}
}

type inertSemanticSession struct{}

func (inertSemanticSession) Augment(req agent.AgentRequest) agent.AgentRequest { return req }
func (inertSemanticSession) Teardown()                                         {}

var _ semantic.Provider = inertSemanticProvider{}
var _ semantic.Session = inertSemanticSession{}

func TestExecute_NoSemanticProviderWired_RequestSemanticStaysZeroValue(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"63": {ID: "63"},
	})
	te.fake.ProgramResult("63", agent.AgentResult{Status: agent.StatusImplemented})

	ctx := context.Background()
	if _, err := te.eng.Execute(ctx, "63", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := te.fake.Invocations()[0].Semantic
	if len(got.MCPServers) != 0 || len(got.NativeServers) != 0 {
		t.Errorf("Semantic descriptor = %+v, want zero value when Engine.Semantic is unset", got)
	}
}

// TestExecute_GoWorkspace_PreparesWithGoplsDetectedServer is issue #128's
// engine-wiring gap: Prepare must be called with the real Detected Servers
// for the repository (internal/lsp.Detect), not the pre-#122 nil
// placeholder, so a Go repository's Provider actually sees {go, gopls} and
// can fill NativeServers for the Claude path.
func TestExecute_GoWorkspace_PreparesWithGoplsDetectedServer(t *testing.T) {
	te := newTestEngine(t, map[string]domain.Issue{
		"64": {ID: "64"},
	})
	if err := os.WriteFile(filepath.Join(te.eng.RepoRoot, "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	te.fake.ProgramResult("64", agent.AgentResult{Status: agent.StatusImplemented})

	sess := &fakeSemanticSession{}
	provider := &fakeSemanticProvider{session: sess}
	te.eng.Semantic = provider

	ctx := context.Background()
	if _, err := te.eng.Execute(ctx, "64", te.base); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	servers := provider.Servers()
	if len(servers) != 1 || servers[0].Language != "go" || len(servers[0].Command) != 1 || servers[0].Command[0] != "gopls" {
		t.Errorf("Prepare's Detected Servers = %+v, want [{go, [gopls]}]", servers)
	}
}
