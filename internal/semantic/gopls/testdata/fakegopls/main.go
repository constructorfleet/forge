// Command fakegopls is a minimal stdio LSP server used only by
// internal/semantic/gopls's tests to exercise Driver's subprocess wiring,
// handshake, crash-restart, and readiness-timeout paths without depending on
// a real gopls binary being installed.
//
// Behavior is selected by the FAKEGOPLS_MODE environment variable:
//
//   - "normal" (default): completes initialize/initialized advertising a
//     fixed capability, then serves until shutdown/exit.
//   - "crash-after-init": completes initialize/initialized, then exits(1)
//     immediately afterward, simulating a server crash.
//   - "hang": never responds to initialize, simulating a wedged server so
//     the driver's readiness timeout fires.
//
// If FAKEGOPLS_SPAWN_LOG names a file, one line is appended to it on every
// invocation so a test can assert how many times the driver spawned this
// fixture (e.g. to verify the restart-once-then-inert limit).
package main

import (
	"context"
	"os"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

func main() {
	if logPath := os.Getenv("FAKEGOPLS_SPAWN_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString("spawn\n")
			_ = f.Close()
		}
	}

	mode := os.Getenv("FAKEGOPLS_MODE")
	if mode == "hang" {
		select {} // never respond; the driver's readiness timeout must fire.
	}

	srv := &fakeServer{crashAfterInit: mode == "crash-after-init"}
	stream := jsonrpc2.NewStream(stdio{})
	ctx, conn, _ := protocol.NewServer(context.Background(), srv, stream)
	_ = ctx

	<-conn.Done()
}

// stdio adapts the fixture process's own standard streams to
// io.ReadWriteCloser for jsonrpc2.NewStream.
type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error {
	_ = os.Stdin.Close()
	return os.Stdout.Close()
}

type fakeServer struct {
	protocol.UnimplementedServer

	crashAfterInit bool
}

func (s *fakeServer) Initialize(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			HoverProvider: protocol.Boolean(true),
		},
	}, nil
}

func (s *fakeServer) Initialized(context.Context, *protocol.InitializedParams) error {
	if s.crashAfterInit {
		os.Exit(1)
	}
	return nil
}

func (s *fakeServer) Shutdown(context.Context) error {
	return nil
}

func (s *fakeServer) Exit(context.Context) error {
	os.Exit(0)
	return nil
}
