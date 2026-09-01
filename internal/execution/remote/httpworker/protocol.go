// Package httpworker implements the one concrete WorkerClient transport:
// a worker daemon (Server) that answers the WorkerClient protocol over
// plain HTTP+JSON, and a Client that drives it. Both stay behind the
// remote.WorkerClient seam (internal/execution/remote); nothing outside
// this package knows the transport is HTTP.
package httpworker

import (
	"github.com/Teagan42/forge/internal/agent"
	"github.com/Teagan42/forge/internal/domain"
	"github.com/Teagan42/forge/internal/execution"
)

// Route paths the Server answers and the Client calls. Each is a single
// POST endpoint carrying a JSON request and response body, except health,
// which is a plain GET.
const (
	pathHealth    = "/v1/health"
	pathPrepare   = "/v1/prepare"
	pathExecute   = "/v1/execute"
	pathAgent     = "/v1/agent"
	pathHeartbeat = "/v1/heartbeat"
	pathResult    = "/v1/result"
	pathCleanup   = "/v1/cleanup"
)

// prepareResponse is pathPrepare's response body.
type prepareResponse struct {
	Handle    string
	Workspace domain.Workspace
}

// executeRequest is pathExecute's request body.
type executeRequest struct {
	Handle  string
	Command execution.Command
}

// executeResponse is pathExecute's response body.
type executeResponse struct {
	Result execution.Result
}

// agentRequest is pathAgent's request body.
type agentRequest struct {
	Handle  string
	Request agent.AgentRequest
}

// agentResponse is pathAgent's response body.
type agentResponse struct {
	Result agent.AgentResult
}

// handleRequest is the request body shared by pathHeartbeat, pathResult,
// and pathCleanup: each needs only the WorkerHandle.
type handleRequest struct {
	Handle string
}

// resultResponse is pathResult's response body, mirroring
// remote.WorkerResult. Bundle marshals as a base64 string, standard
// encoding/json behavior for []byte.
type resultResponse struct {
	Bundle  []byte
	HeadSHA string
}

// errorResponse is the JSON body of any non-2xx response.
type errorResponse struct {
	Error string
}
