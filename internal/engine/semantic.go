package engine

import (
	"context"

	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/config"
	"github.com/Teagan42/forge/internal/lsp"
	"github.com/Teagan42/forge/internal/semantic"
)

// semanticSessionKey identifies the one semantic.Session prepareSemantic
// creates per Issue, keyed by (executionID, issueID) since an issueID alone
// is not unique across Executions.
type semanticSessionKey struct {
	executionID string
	issueID     string
}

// prepareSemantic calls e.Semantic.Prepare (when wired) immediately after
// the Issue's Workspace is created/validated, storing the resulting Session
// so every Agent call across that Issue's repair loop reuses it (see
// augmentSemantic). A no-op when e.Semantic is nil, so callers can
// unconditionally pair this with a deferred teardownSemantic regardless of
// whether Semantic Navigation is wired.
//
// Detected Servers are computed from repoCtx.Languages (Language & server
// detection, #122) through the Language Server Registry built from
// e.Config.LSP, so a Go repository yields {go, gopls} for Prepare to fill
// gaps from.
func (e *Engine) prepareSemantic(ctx context.Context, executionID, issueID, workspacePath string, repoCtx agent.RepositoryContext) {
	if e.Semantic == nil {
		return
	}
	sess := e.Semantic.Prepare(ctx, workspacePath, repoCtx, detectedServers(repoCtx.Languages, e.Config.LSP))
	e.semanticSessions.Store(semanticSessionKey{executionID, issueID}, sess)
}

// detectedServers maps languages through the Language Server Registry built
// from cfg into this package's semantic.DetectedServer shape, translating
// internal/lsp's identical-but-distinct DetectedServer type — internal/lsp
// deliberately stays independent of internal/semantic.
func detectedServers(languages []string, cfg config.LSPConfig) []semantic.DetectedServer {
	detected := lsp.Detect(languages, lsp.NewRegistry(cfg))
	if len(detected) == 0 {
		return nil
	}
	out := make([]semantic.DetectedServer, len(detected))
	for i, d := range detected {
		out[i] = semantic.DetectedServer{Language: d.Language, Command: d.Command}
	}
	return out
}

// teardownSemantic releases the Session prepareSemantic stored for
// (executionID, issueID), if any, and removes it so a later resumption
// cannot reuse a torn-down Session.
func (e *Engine) teardownSemantic(executionID, issueID string) {
	key := semanticSessionKey{executionID, issueID}
	v, ok := e.semanticSessions.LoadAndDelete(key)
	if !ok {
		return
	}
	if sess, ok := v.(semantic.Session); ok {
		sess.Teardown()
	}
}

// augmentSemantic applies the Session prepareSemantic stored for
// (executionID, issueID) to req, if one exists; otherwise it returns req
// unchanged, leaving AgentRequest.Semantic at its zero value.
func (e *Engine) augmentSemantic(executionID, issueID string, req agent.AgentRequest) agent.AgentRequest {
	v, ok := e.semanticSessions.Load(semanticSessionKey{executionID, issueID})
	if !ok {
		return req
	}
	sess, ok := v.(semantic.Session)
	if !ok {
		return req
	}
	return sess.Augment(req)
}
