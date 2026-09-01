package httpworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
	"github.com/Teagan42/forge/internal/workspace"
)

// waitDelay bounds how long a Command waits, after its process exits, for
// the io-copy goroutines feeding stdout/stderr to see EOF. Mirrors
// localhost.Backend's environment.Execute (internal/execution/localhost).
const waitDelay = 2 * time.Second

// errHandleNotFound is returned when a request names a WorkerHandle the
// Server has no prepared Workspace for (an unknown handle, or one already
// cleaned up).
var errHandleNotFound = errors.New("httpworker: unknown workspace handle")

// session is the Server's record of one prepared Workspace: enough to run
// commands and the Agent inside it, and to bundle its result later.
type session struct {
	executionID string
	issueID     string
	base        string
	workspace   domain.Workspace
}

// Server is the concrete worker daemon: it answers the WorkerClient
// protocol over HTTP, driving a real *workspace.Manager and agent.Agent
// against its own local clone of the repository. It is the worker side of
// the one real transport behind the WorkerClient seam (issue #345); the
// Remote backend, WorkerClient interface, and Engine are unchanged by its
// existence.
type Server struct {
	repoRoot   string
	remoteName string
	workspaces *workspace.Manager
	agent      agent.Agent

	mu       sync.Mutex
	sessions map[string]*session

	mux *http.ServeMux
}

// NewServer returns a Server whose primary checkout is repoRoot, an
// existing local clone of the repository the worker has read-only fetch
// access to via the Git remote named remoteName (typically "origin").
// PrepareWorkspace fetches the requested base from remoteName before
// creating a worktree for it. ag runs every RunAgent call.
func NewServer(repoRoot, remoteName string, ag agent.Agent) (*Server, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, errors.New("httpworker: repoRoot must not be empty")
	}
	if strings.TrimSpace(remoteName) == "" {
		return nil, errors.New("httpworker: remoteName must not be empty")
	}
	mgr, err := workspace.NewManager(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("httpworker: new workspace manager: %w", err)
	}
	s := &Server{
		repoRoot:   repoRoot,
		remoteName: remoteName,
		workspaces: mgr,
		agent:      ag,
		sessions:   make(map[string]*session),
	}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET "+pathHealth, s.handleHealth)
	s.mux.HandleFunc("POST "+pathPrepare, s.handlePrepare)
	s.mux.HandleFunc("POST "+pathExecute, s.handleExecute)
	s.mux.HandleFunc("POST "+pathAgent, s.handleAgent)
	s.mux.HandleFunc("POST "+pathHeartbeat, s.handleHeartbeat)
	s.mux.HandleFunc("POST "+pathResult, s.handleResult)
	s.mux.HandleFunc("POST "+pathCleanup, s.handleCleanup)
	return s, nil
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePrepare(w http.ResponseWriter, r *http.Request) {
	var req execution.WorkspaceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	handle, ws, err := s.PrepareWorkspace(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, prepareResponse{Handle: handle, Workspace: ws})
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	var req executeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.Execute(r.Context(), req.Handle, req.Command)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, executeResponse{Result: result})
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.RunAgent(r.Context(), req.Handle, req.Request)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, agentResponse{Result: result})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req handleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Heartbeat(r.Context(), req.Handle); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	var req handleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	bundle, headSHA, err := s.FetchResult(r.Context(), req.Handle)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resultResponse{Bundle: bundle, HeadSHA: headSHA})
}

func (s *Server) handleCleanup(w http.ResponseWriter, r *http.Request) {
	var req handleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Cleanup(r.Context(), req.Handle); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func statusFor(err error) int {
	if errors.Is(err, errHandleNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// PrepareWorkspace fetches req.Base from the Server's configured remote
// (read-only, per the credential boundary: no push credential is ever
// used here) and creates a worktree for it, exactly as the LocalHost
// backend does controller-side, except the Workspace lives on this worker.
func (s *Server) PrepareWorkspace(ctx context.Context, req execution.WorkspaceRequest) (string, domain.Workspace, error) {
	if req.Base != "" {
		if _, err := s.runGit(ctx, "fetch", s.remoteName, req.Base); err != nil {
			return "", domain.Workspace{}, fmt.Errorf("httpworker: fetch base %s: %w", req.Base, err)
		}
	}
	ws, err := s.workspaces.Create(ctx, req.ExecutionID, req.IssueID, req.Base)
	if err != nil {
		return "", domain.Workspace{}, err
	}
	handle := req.ExecutionID + "/" + req.IssueID
	s.mu.Lock()
	s.sessions[handle] = &session{executionID: req.ExecutionID, issueID: req.IssueID, base: req.Base, workspace: ws}
	s.mu.Unlock()
	return handle, ws, nil
}

// Execute runs cmd as a real subprocess rooted at the Workspace handle
// identifies, mirroring localhost.Backend's environment.Execute exactly —
// the same subprocess semantics, just running on the worker instead of the
// controller.
func (s *Server) Execute(ctx context.Context, handle string, cmd execution.Command) (execution.Result, error) {
	sess, ok := s.session(handle)
	if !ok {
		return execution.Result{}, errHandleNotFound
	}

	dir := sess.workspace.Path
	if cmd.WorkDir != "" {
		dir = filepath.Join(dir, cmd.WorkDir)
	}

	result := execution.Result{Name: cmd.Name, Command: cmd.Command, StartedAt: time.Now()}

	var execCmd *exec.Cmd
	if len(cmd.Args) > 0 {
		execCmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	} else {
		execCmd = exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	}
	execCmd.Dir = dir
	execCmd.WaitDelay = waitDelay
	if cmd.Stdin != "" {
		execCmd.Stdin = strings.NewReader(cmd.Stdin)
	}
	if cmd.Env != nil {
		execCmd.Env = cmd.Env
	}
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	runErr := execCmd.Run()
	result.FinishedAt = time.Now()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()

	if runErr == nil {
		result.ExitCode = 0
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, runErr
}

// RunAgent runs the Server's Agent inside the Workspace handle identifies,
// overriding req.WorkspacePath with the worker's own path for that
// Workspace: the controller cannot know the worker's local filesystem
// layout in advance.
func (s *Server) RunAgent(ctx context.Context, handle string, req agent.AgentRequest) (agent.AgentResult, error) {
	sess, ok := s.session(handle)
	if !ok {
		return agent.AgentResult{}, errHandleNotFound
	}
	req.WorkspacePath = sess.workspace.Path
	return s.agent.Execute(ctx, req)
}

// Heartbeat confirms handle identifies a Workspace this Server still has
// prepared. A real worker daemon's controller polls this to keep an
// ExecutionLease from expiring; the daemon itself does nothing further.
func (s *Server) Heartbeat(_ context.Context, handle string) error {
	if _, ok := s.session(handle); !ok {
		return errHandleNotFound
	}
	return nil
}

// FetchResult produces a Git bundle spanning the session's pinned base to
// its current HEAD, plus that HEAD's SHA — the finished work product the
// controller imports (remote.BundlePublisher).
func (s *Server) FetchResult(ctx context.Context, handle string) ([]byte, string, error) {
	sess, ok := s.session(handle)
	if !ok {
		return nil, "", errHandleNotFound
	}

	headSHA, err := s.runGitIn(ctx, sess.workspace.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, "", fmt.Errorf("httpworker: rev-parse HEAD in %s: %w", sess.workspace.Path, err)
	}
	headSHA = strings.TrimSpace(headSHA)

	dir, err := os.MkdirTemp("", "forge-httpworker-bundle-*")
	if err != nil {
		return nil, "", fmt.Errorf("httpworker: create temp dir for bundle: %w", err)
	}
	defer os.RemoveAll(dir)

	bundlePath := filepath.Join(dir, "out.bundle")
	rangeSpec := "HEAD"
	if sess.base != "" {
		rangeSpec = sess.base + "..HEAD"
	}
	if _, err := s.runGitIn(ctx, sess.workspace.Path, "bundle", "create", bundlePath, rangeSpec); err != nil {
		return nil, "", fmt.Errorf("httpworker: bundle create: %w", err)
	}

	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, "", fmt.Errorf("httpworker: read bundle: %w", err)
	}
	return bundle, headSHA, nil
}

// Cleanup removes the Workspace handle identifies and forgets its session.
func (s *Server) Cleanup(ctx context.Context, handle string) error {
	sess, ok := s.session(handle)
	if !ok {
		return errHandleNotFound
	}
	if err := s.workspaces.Cleanup(ctx, sess.executionID, sess.issueID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sessions, handle)
	s.mu.Unlock()
	return nil
}

func (s *Server) session(handle string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[handle]
	return sess, ok
}

func (s *Server) runGit(ctx context.Context, args ...string) (string, error) {
	return s.runGitIn(ctx, s.repoRoot, args...)
}

func (s *Server) runGitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("httpworker: decode request: %w", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}
