// Package gopls owns the process-lifecycle half of Forge's managed gopls
// driver (issue #123): starting a persistent gopls subprocess over stdio,
// completing the initialize/initialized handshake, exposing the server's
// advertised capabilities, and keeping the subprocess alive across a single
// crash. Per-capability query methods (definition, references, ...) are a
// later ticket's concern; this package only gets a live, capability-aware
// connection up and keeps it up.
package gopls

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// Options configures a Driver's subprocess and handshake behavior. All
// fields are used as given — Driver applies no defaulting of its own; see
// internal/config's LSPConfig.ReadinessTimeout and RestartLimit for the
// operator-facing defaults.
type Options struct {
	// Command is the gopls executable and its arguments, e.g.
	// []string{"gopls"}. Must be non-empty.
	Command []string

	// Dir is the workspace root the subprocess is started against and
	// advertised to it as the sole workspace folder.
	Dir string

	// Env is appended to the subprocess's inherited environment
	// (os.Environ()), letting a caller pass through variables a
	// particular language server needs (e.g. GOFLAGS, GOPACKAGESDRIVER).
	Env []string

	// ReadinessTimeout bounds how long Start, and each subsequent restart
	// attempt, waits for the initialize/initialized handshake before that
	// attempt is abandoned and the Driver goes inert.
	ReadinessTimeout time.Duration

	// RestartLimit is how many times a subprocess that crashes after a
	// successful handshake is restarted before the Driver goes
	// permanently inert.
	RestartLimit int
}

// Driver owns a persistent gopls subprocess: startup, the initialize/
// initialized handshake, crash-restart up to Options.RestartLimit, and
// clean shutdown.
//
// Driver never surfaces handshake failures to callers. A readiness timeout,
// a launch failure, or a crash that exhausts RestartLimit all leave the
// Driver inert (Capabilities returns the zero value) rather than returning
// an error, so a Forge-managed gopls that can't come up degrades semantic
// tooling instead of failing the run it's supporting.
type Driver struct {
	opts Options

	mu           sync.Mutex
	capabilities protocol.ServerCapabilities
	restarts     int
	stopping     bool
	cmd          *exec.Cmd
	conn         jsonrpc2.Conn
	server       protocol.Server
	cancelConn   context.CancelFunc
	// monitorDone is closed by monitor once it has reaped cmd, so
	// Shutdown can wait for that instead of calling cmd.Wait() itself —
	// exec.Cmd.Wait must not be called concurrently from two goroutines.
	monitorDone chan struct{}
}

// New returns a Driver for opts. The subprocess is not started until Start
// is called.
func New(opts Options) *Driver {
	return &Driver{opts: opts}
}

// Start launches the gopls subprocess and blocks until the
// initialize/initialized handshake completes or opts.ReadinessTimeout
// elapses, whichever comes first. It never returns an error: see the
// Driver doc comment for how failures are handled.
func (d *Driver) Start(ctx context.Context) {
	d.mu.Lock()
	d.restarts = 0
	d.stopping = false
	d.mu.Unlock()

	d.attemptStart(ctx)
}

// Capabilities returns the server's advertised capabilities from the most
// recent successful handshake, or the zero value if the Driver has never
// completed one or is currently inert.
func (d *Driver) Capabilities() protocol.ServerCapabilities {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.capabilities
}

// Shutdown requests a clean shutdown/exit from a running server and
// terminates the subprocess, waiting up to 5 seconds before killing it
// outright. It is a no-op if the Driver never became ready or is already
// inert.
func (d *Driver) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	d.stopping = true
	conn := d.conn
	server := d.server
	cmd := d.cmd
	cancelConn := d.cancelConn
	done := d.monitorDone
	d.mu.Unlock()

	if conn == nil {
		return nil
	}

	var shutdownErr error
	if server != nil {
		shutdownErr = server.Shutdown(ctx)
		_ = server.Exit(ctx)
	}
	_ = conn.Close()
	if cancelConn != nil {
		cancelConn()
	}

	// monitor reaps cmd once conn.Close() above tears down the read loop;
	// wait for it rather than calling cmd.Wait() here too, and force the
	// issue if the subprocess doesn't exit promptly on its own.
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
	}

	d.mu.Lock()
	d.conn = nil
	d.server = nil
	d.cmd = nil
	d.cancelConn = nil
	d.monitorDone = nil
	d.capabilities = protocol.ServerCapabilities{}
	d.mu.Unlock()

	return shutdownErr
}

// attemptStart spawns the subprocess and attempts the handshake within
// opts.ReadinessTimeout. On success it records the live connection and
// starts the crash monitor. On a readiness timeout it kills the attempt and
// goes inert directly, without consuming restart budget. On any other
// failure (launch error, or the connection dying mid-handshake) it treats
// the attempt as a crash and defers to restartOrInert.
func (d *Driver) attemptStart(ctx context.Context) {
	cmd, rwc, err := d.spawn()
	if err != nil {
		d.restartOrInert()
		return
	}

	connCtx, cancelConn := context.WithCancel(context.Background())
	stream := jsonrpc2.NewStream(rwc)
	_, conn, server := protocol.NewClient(connCtx, &driverClient{}, stream)

	hctx, cancelHandshake := context.WithTimeout(ctx, d.opts.ReadinessTimeout)
	caps, hsErr := handshake(hctx, server, d.opts.Dir)
	timedOut := hctx.Err() == context.DeadlineExceeded
	cancelHandshake()

	if hsErr != nil {
		_ = conn.Close()
		cancelConn()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waitForExit(cmd)

		if timedOut {
			d.goInert()
		} else {
			d.restartOrInert()
		}
		return
	}

	done := make(chan struct{})
	d.mu.Lock()
	d.cmd = cmd
	d.conn = conn
	d.server = server
	d.cancelConn = cancelConn
	d.capabilities = caps
	d.monitorDone = done
	d.mu.Unlock()

	go d.monitor(cmd, conn, done)
}

// monitor waits for the connection to terminate (the subprocess closed its
// stdout, whether by crashing or by a clean Shutdown), reaps the
// subprocess, and, unless Shutdown is what caused it, treats the exit as a
// crash. It is the sole caller of cmd.Wait() for this attempt — closing
// done lets Shutdown wait for that reap instead of racing it with a second
// concurrent Wait call.
func (d *Driver) monitor(cmd *exec.Cmd, conn jsonrpc2.Conn, done chan struct{}) {
	defer close(done)

	<-conn.Done()
	waitForExit(cmd)

	d.mu.Lock()
	stopping := d.stopping
	d.mu.Unlock()
	if stopping {
		return
	}

	d.restartOrInert()
}

// restartOrInert consumes one unit of restart budget and either relaunches
// the subprocess or, once opts.RestartLimit is exhausted, goes permanently
// inert.
func (d *Driver) restartOrInert() {
	d.mu.Lock()
	d.restarts++
	exceeded := d.restarts > d.opts.RestartLimit
	d.mu.Unlock()

	if exceeded {
		d.goInert()
		return
	}

	d.attemptStart(context.Background())
}

// goInert clears any live connection state, leaving Capabilities at its
// zero value until a fresh Start.
func (d *Driver) goInert() {
	d.mu.Lock()
	d.cmd = nil
	d.conn = nil
	d.server = nil
	d.cancelConn = nil
	d.capabilities = protocol.ServerCapabilities{}
	d.mu.Unlock()
}

// spawn starts the gopls subprocess and wires its stdin/stdout into a
// single io.ReadWriteCloser for the jsonrpc2 transport.
func (d *Driver) spawn() (*exec.Cmd, io.ReadWriteCloser, error) {
	cmd := exec.Command(d.opts.Command[0], d.opts.Command[1:]...)
	cmd.Dir = d.opts.Dir
	if len(d.opts.Env) > 0 {
		cmd.Env = append(os.Environ(), d.opts.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	return cmd, stdio{ReadCloser: stdout, WriteCloser: stdin}, nil
}

// waitForExit reaps cmd, tolerating a nil cmd (spawn never succeeded).
func waitForExit(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Wait()
}

// handshake performs the initialize/initialized exchange against server and
// returns the capabilities the server advertised.
func handshake(ctx context.Context, server protocol.Server, dir string) (protocol.ServerCapabilities, error) {
	root := uri.File(dir)
	params := &protocol.InitializeParams{
		Capabilities: protocol.ClientCapabilities{},
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{
				{URI: root, Name: dir},
			}),
		},
	}

	result, err := server.Initialize(ctx, params)
	if err != nil {
		return protocol.ServerCapabilities{}, err
	}
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		return protocol.ServerCapabilities{}, err
	}

	return result.Capabilities, nil
}

// stdio adapts a subprocess's separate stdout/stdin pipes to the single
// io.ReadWriteCloser jsonrpc2.NewStream expects.
type stdio struct {
	io.ReadCloser
	io.WriteCloser
}

func (s stdio) Close() error {
	werr := s.WriteCloser.Close()
	rerr := s.ReadCloser.Close()
	if werr != nil {
		return werr
	}
	return rerr
}

// driverClient is the LSP Client half the Driver presents to the gopls
// subprocess. It has no server-initiated behavior to fulfill yet (no
// window/logMessage surfacing, no workspace/configuration), so it embeds
// UnimplementedClient wholesale; per go.lsp.dev/protocol's contract this
// still correctly absorbs notifications rather than erroring on them.
type driverClient struct {
	protocol.UnimplementedClient
}
