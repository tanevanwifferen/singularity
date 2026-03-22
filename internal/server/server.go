package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"git-frontend/internal/api"
	"git-frontend/internal/engine"
	"git-frontend/internal/git"
	"git-frontend/internal/project"
	"github.com/gorilla/websocket"
)

const version = "0.0.1"

// Server is the HTTP/WebSocket server
type Server struct {
	addr       string
	httpServer *http.Server
	wsUpgrader websocket.Upgrader
	wsClients  map[*websocket.Conn]bool
	wsMux      sync.RWMutex
	repoPath      string
	stopCh        chan struct{}
	engine        *engine.Engine
	projectLoader *project.Loader
}

// New creates a new server
func New(addr string, repoPath string) *Server {
	return &Server{
		addr:     addr,
		repoPath: repoPath,
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		wsClients: make(map[*websocket.Conn]bool),
		stopCh:    make(chan struct{}),
		engine:    engine.New(10),
	}
}

// SetProjectLoader sets the project loader for multi-repo support
func (s *Server) SetProjectLoader(loader *project.Loader) {
	s.projectLoader = loader
}

// Start starts the server
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

// Stop stops the server
func (s *Server) Stop() error {
	close(s.stopCh)

	// Shutdown agent engine
	if s.engine != nil {
		s.engine.Shutdown()
	}

	// Close all WebSocket connections
	s.wsMux.Lock()
	for conn := range s.wsClients {
		conn.Close()
	}
	s.wsClients = make(map[*websocket.Conn]bool)
	s.wsMux.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

// registerRoutes registers all HTTP routes
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// CORS middleware
	withCORS := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			h(w, r)
		}
	}

	// API routes
	mux.HandleFunc("/api/status", withCORS(s.handleStatus))
	mux.HandleFunc("/api/repo/open", withCORS(s.handleRepoOpen))
	mux.HandleFunc("/api/repo/info", withCORS(s.handleRepoInfo))
	mux.HandleFunc("/api/repo", withCORS(s.handleRepoInfo)) // alias for browser access
	mux.HandleFunc("/api/branch/compare", withCORS(s.handleBranchCompare))
	mux.HandleFunc("/api/branch/diff", withCORS(s.handleBranchDiff))
	mux.HandleFunc("/api/commit/message", withCORS(s.handleCommitMessage))
	mux.HandleFunc("/api/mr/create", withCORS(s.handleMRCreate))
	mux.HandleFunc("/api/forge/auth", withCORS(s.handleForgeAuth))
	
	// Project routes
	mux.HandleFunc("/api/project/list", withCORS(s.handleProjectList))
	mux.HandleFunc("/api/project/load", withCORS(s.handleProjectLoad))
	mux.HandleFunc("/api/project/status", withCORS(s.handleProjectStatus))
	mux.HandleFunc("/api/project/refresh", withCORS(s.handleProjectRefresh))
	mux.HandleFunc("/api/project/branch/check", withCORS(s.handleProjectBranchCheck))
	mux.HandleFunc("/api/project/branch/compare", withCORS(s.handleProjectBranchCompare))
	mux.HandleFunc("/api/project/context", withCORS(s.handleProjectContext))

	// Agent engine routes
	mux.HandleFunc("/api/agent/start", withCORS(s.handleAgentStart))
	mux.HandleFunc("/api/agent/message", withCORS(s.handleAgentMessage))
	mux.HandleFunc("/api/agent/status", withCORS(s.handleAgentStatus))
	mux.HandleFunc("/api/agent/output", withCORS(s.handleAgentOutput))
	mux.HandleFunc("/api/agent/kill", withCORS(s.handleAgentKill))
	mux.HandleFunc("/api/agent/list", withCORS(s.handleAgentList))
	mux.HandleFunc("/api/agent/stats", withCORS(s.handleAgentStats))

	// WebSocket route
	mux.HandleFunc("/ws", s.handleWebSocket)
	
	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
}

// handleStatus handles GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := api.StatusResponse{
		Version: version,
		Server:  "git-frontend-api",
	}

	if s.repoPath != "" {
		resp.RepoPath = s.repoPath
		if repo, err := git.OpenRepo(s.repoPath); err == nil {
			resp.RepoInfo = repo
		}
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: resp})
}

// handleRepoOpen handles POST /api/repo/open
func (s *Server) handleRepoOpen(w http.ResponseWriter, r *http.Request) {
	var req api.RepoRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	path := req.Path
	if path == "" {
		// Find repo from current directory
		cwd, err := os.Getwd()
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "no path provided and cannot get cwd")
			return
		}
		path = cwd
	}

	// Find repo if path is not a repo
	if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		repoPath, err := git.FindRepo(path)
		if err != nil {
			s.writeError(w, http.StatusNotFound, "no git repository found")
			return
		}
		path = repoPath
	}

	s.repoPath = path
	repo, err := git.OpenRepo(path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: repo})
}

// handleRepoInfo handles GET /api/repo/info
func (s *Server) handleRepoInfo(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.repoPath
	}

	if path == "" {
		s.writeError(w, http.StatusBadRequest, "no repo path provided")
		return
	}

	repo, err := git.OpenRepo(path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: repo})
}

// handleBranchCompare handles POST /api/branch/compare
func (s *Server) handleBranchCompare(w http.ResponseWriter, r *http.Request) {
	var req api.BranchComparisonRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	path := req.RepoPath
	if path == "" {
		path = s.repoPath
	}

	comparison, err := git.CompareBranches(path, req.BranchA, req.BranchB)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: comparison})
}

// handleBranchDiff handles POST /api/branch/diff
func (s *Server) handleBranchDiff(w http.ResponseWriter, r *http.Request) {
	var req api.BranchDiffRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	path := req.RepoPath
	if path == "" {
		path = s.repoPath
	}

	diff, err := git.GetBranchDiff(path, req.BranchA, req.BranchB)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: diff})
}

// handleCommitMessage handles POST /api/commit/message
func (s *Server) handleCommitMessage(w http.ResponseWriter, r *http.Request) {
	var req api.CommitMessageRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	path := req.RepoPath
	if path == "" {
		path = s.repoPath
	}

	msg, err := git.GenerateCommitMessage(path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: msg})
}

// handleMRCreate handles POST /api/mr/create
func (s *Server) handleMRCreate(w http.ResponseWriter, r *http.Request) {
	var req api.MRRequest
	if err := s.parseJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	path := req.RepoPath
	if path == "" {
		path = s.repoPath
	}

	mr, err := git.CreateMR(path, req.SourceBranch, req.TargetBranch, req.Title, req.Description, req.Reviewers)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: mr})
}

// handleForgeAuth handles GET /api/forge/auth
func (s *Server) handleForgeAuth(w http.ResponseWriter, r *http.Request) {
	auth, err := git.DetectForgeAuth()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, api.APIResponse{Success: true, Data: auth})
}

// handleWebSocket handles WebSocket connections
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	s.wsMux.Lock()
	s.wsClients[conn] = true
	s.wsMux.Unlock()

	defer func() {
		s.wsMux.Lock()
		delete(s.wsClients, conn)
		s.wsMux.Unlock()
		conn.Close()
	}()

	// Start heartbeat and event reader
	go s.wsHeartbeat(conn)
	s.wsReader(conn)
}

// wsHeartbeat sends periodic pings to keep connection alive
func (s *Server) wsHeartbeat(conn *websocket.Conn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

// wsReader reads and processes WebSocket messages
func (s *Server) wsReader(conn *websocket.Conn) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Parse incoming message
		var wsMsg api.WSMessage
		if err := json.Unmarshal(msg, &wsMsg); err != nil {
			s.wsSendError(conn, "invalid message format")
			continue
		}

		// Handle different message types
		s.handleWSMessage(conn, &wsMsg)
	}
}

// handleWSMessage processes incoming WebSocket messages
func (s *Server) handleWSMessage(conn *websocket.Conn, msg *api.WSMessage) {
	switch msg.Type {
	case "subscribe":
		// Subscribe to events (already connected)
		s.wsSend(conn, api.WSMessage{Type: "subscribed", Payload: map[string]string{"status": "ok"}})
	case "refresh_repo":
		// Force refresh repo info and broadcast
		if s.repoPath != "" {
			repo, err := git.OpenRepo(s.repoPath)
			if err == nil {
				s.wsBroadcast(api.WSMessage{Type: api.WSEventRepoUpdate, Payload: repo})
			}
		}
	default:
		s.wsSendError(conn, fmt.Sprintf("unknown message type: %s", msg.Type))
	}
}

// wsSend sends a WebSocket message
func (s *Server) wsSend(conn *websocket.Conn, msg api.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling WS message: %v", err)
		return
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("Error sending WS message: %v", err)
	}
}

// wsSendError sends an error message
func (s *Server) wsSendError(conn *websocket.Conn, errMsg string) {
	s.wsSend(conn, api.WSMessage{Type: api.WSEventError, Payload: map[string]string{"error": errMsg}})
}

// wsBroadcast sends a message to all connected clients
func (s *Server) wsBroadcast(msg api.WSMessage) {
	s.wsMux.RLock()
	defer s.wsMux.RUnlock()

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling WS broadcast: %v", err)
		return
	}

	for conn := range s.wsClients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("Error broadcasting to client: %v", err)
		}
	}
}

// BroadcastRepoUpdate broadcasts a repo update event
func (s *Server) BroadcastRepoUpdate(repo *git.RepoInfo) {
	s.wsBroadcast(api.WSMessage{Type: api.WSEventRepoUpdate, Payload: repo})
}

// BroadcastBranchUpdate broadcasts a branch update event
func (s *Server) BroadcastBranchUpdate(branch string) {
	s.wsBroadcast(api.WSMessage{Type: api.WSEventBranchUpdate, Payload: map[string]string{"branch": branch}})
}

// Helper methods

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, api.APIResponse{Success: false, Error: msg})
}

func (s *Server) parseJSON(r *http.Request, v interface{}) error {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return fmt.Errorf("content-type must be application/json")
	}
	return json.NewDecoder(r.Body).Decode(v)
}
