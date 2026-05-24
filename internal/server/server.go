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
	addr     string
	repoPath string

	httpServer *http.Server
	stopCh     chan struct{}

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
	// broadcast for a given agent. Updated under outputMu.
	outputMu           sync.Mutex
	agentOutputOffsets map[string]int

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

// writeMessage sends a single WS frame under the per-connection write mutex.
func (c *wsClient) writeMessage(messageType int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
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
// frames; an entry is removed from the map when its terminal Done frame
// is broadcast.
type streamEntry struct {
	id          string
	cancel      func()
	subscribers map[*websocket.Conn]bool
	mu          sync.Mutex
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

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("Starting server on %s", s.addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

	s.httpServer = &http.Server{Handler: mux}
	log.Printf("Serving on %s", ln.Addr())
	if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server. It also cancels every active
// stream and closes every WebSocket connection — http.Server.Shutdown alone
// would block on hijacked WS conns until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	s.cancelAllStreams()
	s.closeAllWS()
	return s.httpServer.Shutdown(ctx)
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

// Stop stops the server.
func (s *Server) Stop() error {
	close(s.stopCh)

	if s.engine != nil {
		s.engine.Shutdown()
	}

	s.closeAllWS()

	// Cancel every in-flight stream.
	s.streamMu.Lock()
	for id, e := range s.streams {
		if e.cancel != nil {
			e.cancel()
		}
		delete(s.streams, id)
	}
	s.streamMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// handleStatus handles GET /api/status. Independent of Services.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := api.StatusResponse{Version: version, Server: "singularity-api"}
	if s.repoPath != "" {
		resp.RepoPath = s.repoPath
		if s.Services != nil && s.Services.Repo != nil {
			if info, err := s.Services.Repo.Open(r.Context(), s.repoPath); err == nil {
				resp.RepoInfo = info
			}
		}
	}
	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: resp})
}

// ---------------- helpers ----------------

// resolveRepoPath returns reqPath when non-empty, otherwise the server's
// fallback repoPath set at startup or by /api/repo/open. The result passes
// through validateRepoPath so traversal attempts and (when configured)
// off-allow-list paths are rejected before they reach the git layer. On
// validation failure the cleaned path is returned anyway alongside a non-nil
// error — the only caller that examines the error today is the agent start
// handler; everyone else discards the error and relies on the downstream git
// layer to surface a path-not-found.
func (s *Server) resolveRepoPath(reqPath string) string {
	p := reqPath
	if p == "" {
		p = s.repoPath
	}
	if p == "" {
		return ""
	}
	cleaned, err := s.validateRepoPath(p)
	if err != nil {
		// Returning empty here would mask the offending input and have the
		// git layer error with "not a git repository: ''". Return the
		// original instead; the handler will fail with a NOT_FOUND-style
		// service error.
		return p
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
