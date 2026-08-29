package planninge2e_test

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/engine"
	"github.com/Teagan42/forge/internal/gittest"
	"github.com/Teagan42/forge/internal/planning"
	"github.com/Teagan42/forge/internal/planningagent"
	"github.com/Teagan42/forge/internal/specengine"
	"github.com/Teagan42/forge/internal/storage"
	"github.com/Teagan42/forge/internal/tracker"
	"github.com/Teagan42/forge/internal/workspace"
)

// ---------------------------------------------------------------------------
// In-memory Planning Artifact store
// ---------------------------------------------------------------------------

// memLoader is the local, in-memory stand-in for Forge's canonical
// filesystem artifact storage (.forge/features/<id>/{goal.md,decisions/*.md,
// spec.md,ticket-plan.md}). It satisfies both specengine.ArtifactLoader and
// replan.DecisionStore, so one object plays "the Feature's planning files"
// for every stage of the pipeline.
//
// Everything it holds is *local* state by construction — which is exactly
// what scenario 14 wipes to simulate a lost .forge/ directory while the
// tracker (canonical, remote) survives.
type memLoader struct {
	mu         sync.Mutex
	goal       *planning.Artifact
	decisions  map[string]*planning.Artifact
	spec       *planning.Artifact
	ticketPlan *planning.Artifact
}

func newMemLoader(goal *planning.Artifact) *memLoader {
	return &memLoader{goal: goal, decisions: map[string]*planning.Artifact{}}
}

func (m *memLoader) LoadGoal(context.Context, string) (*planning.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.goal == nil {
		return nil, fmt.Errorf("memLoader: no goal artifact")
	}
	return m.goal, nil
}

func (m *memLoader) LoadDecisions(context.Context, string) (map[string]*planning.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*planning.Artifact, len(m.decisions))
	for id, d := range m.decisions {
		out[id] = d
	}
	return out, nil
}

func (m *memLoader) SaveDecision(_ context.Context, _ string, decisionID string, decision *planning.Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decisions[decisionID] = decision
	return nil
}

func (m *memLoader) SaveSpec(_ context.Context, _ string, spec *planning.Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spec = spec
	return nil
}

func (m *memLoader) LoadSpec(context.Context, string) (*planning.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spec, nil
}

func (m *memLoader) SaveTicketPlan(_ context.Context, _ string, tp *planning.Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ticketPlan = tp
	return nil
}

// persist is a wayfinding.Persist that writes through to the loader,
// mirroring how a real run writes each Decision to disk the moment it
// changes.
func (m *memLoader) persist(id string, artifact *planning.Artifact) error {
	return m.SaveDecision(context.Background(), "", id, artifact)
}

// wipe discards every locally-held Planning Artifact, simulating a lost
// .forge/ directory (scenario 14). Nothing on the tracker is touched.
func (m *memLoader) wipe() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.goal = nil
	m.decisions = map[string]*planning.Artifact{}
	m.spec = nil
	m.ticketPlan = nil
}

var (
	_ specengine.ArtifactLoader = (*memLoader)(nil)
)

// ---------------------------------------------------------------------------
// Artifact fixtures
// ---------------------------------------------------------------------------

// newGoal builds a goal Artifact with a correctly recorded content revision,
// so planning.Stale is false for it from the start.
func newGoal(body string) *planning.Artifact {
	a := &planning.Artifact{
		Kind:     planning.KindGoal,
		State:    "approved",
		Sections: []planning.Section{{Heading: "Goal", Body: body}},
	}
	a.Revision = planning.ComputeRevision(a)
	a.ApprovedRevision = a.Revision
	return a
}

// newResolvedDecision builds an already-resolved, already-approved Decision,
// exactly as decisiongraph.ApplyResolution leaves one.
func newResolvedDecision(question, outcome string, derivedFrom ...planning.DerivedFromEntry) *planning.Artifact {
	a := &planning.Artifact{
		Kind:        planning.KindDecision,
		State:       "resolved",
		DerivedFrom: derivedFrom,
		Sections: []planning.Section{
			{Heading: "Question", Body: question},
			{Heading: "Outcome", Body: outcome},
		},
	}
	a.Revision = planning.ComputeRevision(a)
	a.ApprovedRevision = a.Revision
	return a
}

// approveArtifact stamps a human approval onto a, binding it to a's current
// content revision — the same binding `forge approve` performs, and the only
// thing planning.Approved consults.
func approveArtifact(a *planning.Artifact) {
	a.Revision = planning.ComputeRevision(a)
	a.State = "approved"
	a.ApprovedRevision = a.Revision
}

// derivedRevision returns the revision a recorded for the DerivedFrom entry
// named id, and whether such an entry exists.
func derivedRevision(a *planning.Artifact, id string) (string, bool) {
	for _, d := range a.DerivedFrom {
		if d.ID == id {
			return d.Revision, true
		}
	}
	return "", false
}

// sectionBody returns a's body for heading, or "" when it has no such
// section.
func sectionBody(a *planning.Artifact, heading string) string {
	for _, s := range a.Sections {
		if s.Heading == heading {
			return s.Body
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Fake backend scripting helpers
// ---------------------------------------------------------------------------

// fenced wraps raw JSON in the fenced ```json block every planning contract's
// structured-result extractor (planningagent.InvokeStructured) scans for.
func fenced(raw string) string {
	return "```json\n" + raw + "\n```\n"
}

// invocationKeys returns the ordered list of stage keys the backend was
// invoked with, which is the pipeline's own execution trace.
func invocationKeys(backend *planningagent.FakeBackend) []string {
	inv := backend.Invocations()
	keys := make([]string, 0, len(inv))
	for _, i := range inv {
		keys = append(keys, i.Key)
	}
	return keys
}

// countKey returns how many times key was invoked.
func countKey(backend *planningagent.FakeBackend, key string) int {
	n := 0
	for _, k := range invocationKeys(backend) {
		if k == key {
			n++
		}
	}
	return n
}

var reResolutionTarget = regexp.MustCompile(`## Decision to resolve \(([^)]+)\)`)

// resolvedTargets returns, in call order, the Decision ID each
// decision-resolution invocation was asked to resolve — read back out of the
// prompt the contract actually built, so the assertion is about what the
// pipeline really did rather than about the test's own bookkeeping.
func resolvedTargets(backend *planningagent.FakeBackend) []string {
	var out []string
	for _, inv := range backend.Invocations() {
		if inv.Key != "decision-resolution" {
			continue
		}
		m := reResolutionTarget.FindStringSubmatch(inv.Prompt)
		if m == nil {
			out = append(out, "<unparsed>")
			continue
		}
		out = append(out, m[1])
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Tracker + runtime harness
// ---------------------------------------------------------------------------

// featureTracker is tracker.FakeTracker plus the ability to post a comment
// as a named *human* author. FakeTracker.AddComment always attributes to
// "forge-bot" (correctly: it models Forge's own posting identity), and
// wayfinding.ResumeDecision deliberately ignores comments authored by the
// bot that posted the needs-human checkpoint — so a resume scenario needs a
// way to speak as someone else. Everything else delegates, so the Issues,
// labels, and bot comments a scenario asserts on are the real FakeTracker's.
type featureTracker struct {
	*tracker.FakeTracker

	mu    sync.Mutex
	human map[string][]tracker.Comment
}

func newFeatureTracker() *featureTracker {
	return &featureTracker{FakeTracker: tracker.NewFakeTracker(), human: map[string][]tracker.Comment{}}
}

// PostHuman appends a human-authored comment on id at the given tracker
// clock time.
func (f *featureTracker) PostHuman(id, author, body string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.human[id] = append(f.human[id], tracker.Comment{Author: author, Body: body, CreatedAt: at})
}

func (f *featureTracker) GetComments(ctx context.Context, id string) ([]tracker.Comment, error) {
	base, err := f.FakeTracker.GetComments(ctx, id)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(base, f.human[id]...), nil
}

var _ tracker.Tracker = (*featureTracker)(nil)

// openStore opens a fresh, migrated SQLite store in its own temp directory —
// Forge's local runtime state, and nothing else.
func openStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "forge.db")
	store, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// phase1 bundles a Phase 1 execution engine wired to a tracker, so a
// materialized Issue graph can be handed straight over the Phase 1 handoff.
type phase1 struct {
	eng   *engine.Engine
	store *storage.SQLiteStore
	agent *agent.FakeAgent
	base  string
}

// newPhase1 builds a Phase 1 Engine over trk, a fresh store, and a fresh
// temp git repository, exactly as internal/engine's own tests do.
func newPhase1(t *testing.T, trk engine.IssueFetcher) phase1 {
	t.Helper()
	repoRoot, base := gittest.NewTempRepo(t)
	store := openStore(t)
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	fakeAgent := agent.NewFakeAgent()
	eng := engine.New(store, trk, mgr, fakeAgent, config.Default(), repoRoot)
	return phase1{eng: eng, store: store, agent: fakeAgent, base: base}
}

// issueBody fetches an Issue's current body straight from the tracker.
func issueBody(t *testing.T, trk *featureTracker, id string) string {
	t.Helper()
	issue, err := trk.GetIssue(context.Background(), id)
	if err != nil {
		t.Fatalf("GetIssue %s: %v", id, err)
	}
	return issue.Body
}

func mustContain(t *testing.T, what, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s does not contain %q:\n%s", what, needle, haystack)
	}
}
