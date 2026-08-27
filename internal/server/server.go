// Package server hosts the HTTP+WebSocket surface of the singularity daemon.
// Every route is documented in docs/design/WIRE-CONTRACT.md; this package
// dispatches each route to its matching service.Services method.
//
// Handlers depend on Server.Services, a *service.Services constructed by the
// daemon and injected via SetServices. While Services is nil (e.g. during the
// Phase B → Phase C transition before local-coder lands the impls) every
// service-routed handler returns 503 UNAVAILABLE. The WS event plumbing and
// engine callbacks operate independently of Services so the agent surface
// stays live during that window.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/project"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

const version = "0.0.1"

// Server is the HTTP/WebSocket server.
type Server struct {
	addr string

	// repoPath is the fallback repo used when a request omits repo_path.
	// Written by /api/repo/open while other handlers read it concurrently,
	// so all access goes through getRepoPath/setRepoPath under repoMu.
	repoMu   sync.RWMutex
	repoPath string

	// httpMu guards httpServer: Serve/Start assign it from their own
	// goroutine while Shutdown/Stop read it from the signal handler.
	httpMu     sync.Mutex
	httpServer *http.Server
	stopCh     chan struct{}
	stopOnce   sync.Once

	wsUpgrader websocket.Upgrader
	wsClients  map[*websocket.Conn]*wsClient
	wsMux      sync.RWMutex

	// engine is retained for WS event broadcasting only — handlers route
	// agent operations through Services.Agent. wireEngineCallbacks registers
	// the OnAgentUpdate hook that drives agent_output / agent_complete /
	// agent_error broadcasts.
	engine        *engine.Engine
	projectLoader *project.Loader

	// Services is the dispatch target for every HTTP handler. May be nil; in
	// that case all service-routed handlers reply with 503 UNAVAILABLE.
	Services *service.Services

	// agentOutputOffsets tracks the highest output entry offset already
	// broadcast for a given agent; terminalBroadcast marks agents whose
	// terminal lifecycle event (complete/error) has already been sent, so
	// overlapping debounce callbacks can't emit it twice. Both under outputMu.
	outputMu           sync.Mutex
	agentOutputOffsets map[string]int
	terminalBroadcast  map[string]bool

	// streams is the per-stream registry of channels created by the
	// **stream** endpoints; subscribe_stream / cancel_stream WS messages
	// look streams up here.
	streamMu sync.Mutex
	streams  map[string]*streamEntry

	// authToken, when non-empty, gates every HTTP and WS request through the
	// requireToken middleware. Set via SetAuthToken; the daemon sets it for
	// TCP listeners and leaves it empty for unix-socket listeners.
	authToken string

	// isUnixListener is true when the underlying listener is AF_UNIX. The WS
	// upgrader's CheckOrigin uses this to relax origin checks for the local
	// case (no browser can reach a unix socket).
	isUnixListener bool

	// allowedRoots optionally constrains validateRepoPath; empty means any
	// absolute path is accepted.
	allowedRoots []string
}

// wsClient wraps a *websocket.Conn with a write mutex. gorilla/websocket
// forbids concurrent writes to the same Conn; every site that writes to a
// connection must go through one of the writeXxx methods on this struct so
// the write mutex serializes them.
type wsClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

// wsWriteWait bounds every data write to a WS connection. Without it a peer
// that stops reading (dead TCP connection, full send buffer) blocks the
// write forever while holding writeMu — which then stalls every broadcast
// path in the daemon behind this one connection.
const wsWriteWait = 10 * time.Second

// writeMessage sends a single WS frame under the per-connection write mutex.
func (c *wsClient) writeMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return c.conn.WriteMessage(messageType, data)
}

// writeControl sends a WS control frame under the per-connection write mutex.
func (c *wsClient) writeControl(messageType int, data []byte, deadline time.Time) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteControl(messageType, data, deadline)
}

// streamEntry captures one in-flight server-side stream. Cancel is the
// service-supplied closure that tears down the underlying subscription.
// Subscribers is the set of WS connections that asked for this stream's
// frames.
//
// Because the stream starts pumping the moment the HTTP handler returns —
// before the client's subscribe_stream can possibly arrive — frames emitted
// while no subscriber has ever attached are buffered in pending and replayed
// to the first subscriber. Finished entries linger for streamRetention so a
// subscribe that races the terminal frame still gets the replay instead of
// hanging forever on a deleted stream.
type streamEntry struct {
	id          string
	cancel      func()
	subscribers map[*websocket.Conn]bool
	mu          sync.Mutex

	// pending holds wire-encoded frames broadcast before the first
	// subscriber attached; nil once replayed. Guarded by mu.
	pending [][]byte
	// hasSubscriber flips to true on the first subscribe and never resets.
	// Guarded by mu.
	hasSubscriber bool
}

// New creates a new server. Engine is created up-front so WS callbacks can
// be wired even before SetServices is invoked; daemon owners that bring
// their own engine should construct Services with the same engine instance.
func New(addr string, repoPath string) *Server {
	s := &Server{
		addr:               addr,
		repoPath:           repoPath,
		wsClients:          make(map[*websocket.Conn]*wsClient),
		stopCh:             make(chan struct{}),
		engine:             engine.New(10),
		agentOutputOffsets: make(map[string]int),
		terminalBroadcast:  make(map[string]bool),
		streams:            make(map[string]*streamEntry),
	}
	s.wsUpgrader = websocket.Upgrader{
		CheckOrigin:     s.checkOrigin,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	s.wireEngineCallbacks()
	return s
}

// SetAuthToken installs a bearer token that the requireToken middleware will
// enforce on every HTTP and WS request. Pass an empty string to disable
// auth (the default for unix-socket listeners).
func (s *Server) SetAuthToken(token string) {
	s.authToken = token
}

// SetUnixListener flags the server as bound to an AF_UNIX listener. The WS
// origin check skips the same-origin enforcement in that case because no
// browser can reach a unix socket.
func (s *Server) SetUnixListener(unix bool) {
	s.isUnixListener = unix
}

// SetAllowedRoots configures the optional allow-list used by
// validateRepoPath. Empty (the default) means any absolute path is accepted.
func (s *Server) SetAllowedRoots(roots []string) {
	s.allowedRoots = roots
}

// checkOrigin is the WS upgrade origin guard. For unix-socket listeners we
// allow everything (no browser path). For TCP listeners we require the
// Origin header's host to match the request Host (or be empty, which
// indicates a non-browser client).
func (s *Server) checkOrigin(r *http.Request) bool {
	if s.isUnixListener {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// SetProjectLoader sets the project loader for multi-repo support.
// Retained for backward compatibility with cmd/singularity wiring; new code
// should pass a project.Loader to the service.local constructor instead.
func (s *Server) SetProjectLoader(loader *project.Loader) {
	s.projectLoader = loader
}

// SetServices wires the service.Services dispatch target. Safe to call
// multiple times; the last value wins.
func (s *Server) SetServices(svc *service.Services) {
	s.Services = svc
}

// Engine returns the server's engine instance. Daemon wiring uses this to
// construct service.local with the same engine the server already owns.
func (s *Server) Engine() *engine.Engine {
	return s.engine
}

// ProjectLoader returns the server's project loader (may be nil).
func (s *Server) ProjectLoader() *project.Loader {
	return s.projectLoader
}

// Start starts the server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	s.httpMu.Lock()
	s.httpServer = srv
	s.httpMu.Unlock()

	log.Printf("Starting server on %s", s.addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Serve runs the HTTP server on a caller-supplied listener. Used by the
// daemon command (internal/daemon) to bind a unix socket. ReadTimeout and
// WriteTimeout are deliberately unset because WebSocket connections are
// long-lived; per-handler timeouts apply where relevant. Blocks until the
// listener is closed or Shutdown is called.
func (s *Server) Serve(ln net.Listener) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	srv := &http.Server{Handler: mux}
	s.httpMu.Lock()
	s.httpServer = srv
	s.httpMu.Unlock()
	log.Printf("Serving on %s", ln.Addr())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server. It also cancels every active
// stream and closes every WebSocket connection — http.Server.Shutdown alone
// would block on hijacked WS conns until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	s.httpMu.Lock()
	srv := s.httpServer
	s.httpMu.Unlock()
	if srv == nil {
		return nil
	}
	s.cancelAllStreams()
	s.closeAllWS()
	return srv.Shutdown(ctx)
}

// cancelAllStreams invokes every active stream's cancel closure and clears
// the registry. Safe to call multiple times.
func (s *Server) cancelAllStreams() {
	s.streamMu.Lock()
	entries := s.streams
	s.streams = make(map[string]*streamEntry)
	s.streamMu.Unlock()
	for _, e := range entries {
		if e.cancel != nil {
			e.cancel()
		}
	}
}

// closeAllWS forces every WS connection closed so hijacked handlers return
// promptly and http.Server.Shutdown can drain.
func (s *Server) closeAllWS() {
	s.wsMux.Lock()
	clients := s.wsClients
	s.wsClients = make(map[*websocket.Conn]*wsClient)
	s.wsMux.Unlock()
	for conn := range clients {
		_ = conn.Close()
	}
}

// Stop stops the server. Idempotent and safe to call before Start/Serve.
func (s *Server) Stop() error {
	s.stopOnce.Do(func() { close(s.stopCh) })

	if s.engine != nil {
		s.engine.Shutdown()
	}

	s.closeAllWS()
	s.cancelAllStreams()

	s.httpMu.Lock()
	srv := s.httpServer
	s.httpMu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// handleStatus handles GET /api/status. Independent of Services.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := api.StatusResponse{Version: version, Server: "singularity-api"}
	if repoPath := s.getRepoPath(); repoPath != "" {
		resp.RepoPath = repoPath
		if s.Services != nil && s.Services.Repo != nil {
			if info, err := s.Services.Repo.Open(r.Context(), repoPath); err == nil {
				resp.RepoInfo = info
			}
		}
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: resp})
}

// ---------------- helpers ----------------

// getRepoPath returns the fallback repo path under repoMu.
func (s *Server) getRepoPath() string {
	s.repoMu.RLock()
	defer s.repoMu.RUnlock()
	return s.repoPath
}

// setRepoPath updates the fallback repo path under repoMu.
func (s *Server) setRepoPath(p string) {
	s.repoMu.Lock()
	s.repoPath = p
	s.repoMu.Unlock()
}

// resolveRepoPath returns reqPath when non-empty, otherwise the server's
// fallback repoPath set at startup or by /api/repo/open. The result passes
// through validateRepoPath so traversal attempts and (when configured)
// off-allow-list paths are rejected before they reach the git layer. On
// validation failure an empty string is returned — never the raw input, which
// would hand a traversal-laden or off-allow-list path straight to the git
// layer. Handlers then fail with a path-not-found-style service error.
func (s *Server) resolveRepoPath(reqPath string) string {
	p := reqPath
	if p == "" {
		p = s.getRepoPath()
	}
	if p == "" {
		return ""
	}
	cleaned, err := s.validateRepoPath(p)
	if err != nil {
		return ""
	}
	return cleaned
}

// requireServices writes a 503 UNAVAILABLE response and returns false when
// Services is nil. Every service-routed handler calls this first; it keeps
// the build green during the Phase B → Phase C transition.
func (s *Server) requireServices(w http.ResponseWriter) bool {
	if s.Services == nil {
		s.writeCoded(w, api.ErrCodeUnavailable, "service layer not wired")
		return false
	}
	return true
}

// requireMethod writes a 405 response and returns false when the request
// method does not match.
func (s *Server) requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		s.writeError(w, http.StatusMethodNotAllowed, method+" required")
		return false
	}
	return true
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, api.APIResponse{Success: false, Error: msg})
}

// writeCoded writes a coded error response. The HTTP status is derived from
// the code via api.HTTPStatusForCode.
func (s *Server) writeCoded(w http.ResponseWriter, code, msg string) {
	s.writeJSON(w, api.HTTPStatusForCode(code), api.APIResponse{Success: false, Error: msg, Code: code})
}

// writeServiceErr maps a service sentinel to the matching wire code and
// writes the coded response. Unknown errors map to INTERNAL.
func (s *Server) writeServiceErr(w http.ResponseWriter, err error) {
	code := codeForServiceErr(err)
	s.writeJSON(w, api.HTTPStatusForCode(code), api.APIResponse{Success: false, Error: err.Error(), Code: code})
}

// codeForServiceErr maps a service sentinel (or sentinel-wrapping error) to
// its wire code constant. Errors that don't match any sentinel fall back to
// INTERNAL.
func codeForServiceErr(err error) string {
	switch {
	case errors.Is(err, service.ErrMRAlreadyExists):
		return api.ErrCodeMRAlreadyExists
	case errors.Is(err, service.ErrNotFound):
		return api.ErrCodeNotFound
	case errors.Is(err, service.ErrConflict):
		return api.ErrCodeConflict
	case errors.Is(err, service.ErrAgentLimit):
		return api.ErrCodeAgentLimit
	case errors.Is(err, service.ErrNoForge):
		return api.ErrCodeNoForge
	case errors.Is(err, service.ErrRebaseInProgress):
		return api.ErrCodeRebaseInProgress
	case errors.Is(err, service.ErrNoRebaseInProgress):
		return api.ErrCodeNoRebaseInProgress
	case errors.Is(err, service.ErrPermissionDenied):
		return api.ErrCodePermissionDenied
	case errors.Is(err, service.ErrUnavailable):
		return api.ErrCodeUnavailable
	case errors.Is(err, service.ErrCanceled):
		return api.ErrCodeCanceled
	}
	return api.ErrCodeInternal
}

// maxBodyBytes caps inbound JSON bodies. 1 MiB is well above any realistic
// request shape we send and well below "we just OOM'd the daemon".
const maxBodyBytes = 1 << 20

// parseJSON decodes the request body into v. It enforces a Content-Type of
// application/json and a hard cap on body size via http.MaxBytesReader.
func (s *Server) parseJSON(r *http.Request, v interface{}) error {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return fmt.Errorf("content-type must be application/json")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	return json.NewDecoder(r.Body).Decode(v)
}
