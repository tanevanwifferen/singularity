package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// SessionState represents the state of a Claude Code session
type SessionState int

const (
	SessionIdle SessionState = iota
	SessionSpawning
	SessionRunning
	SessionWaiting
	SessionDone
	SessionError
)

func (s SessionState) String() string {
	switch s {
	case SessionIdle:
		return "idle"
	case SessionSpawning:
		return "spawning"
	case SessionRunning:
		return "running"
	case SessionWaiting:
		return "waiting"
	case SessionDone:
		return "done"
	case SessionError:
		return "error"
	default:
		return "unknown"
	}
}

// Session represents a Claude Code session
type Session struct {
	ID        string       `json:"id"`
	Project   string       `json:"project"`
	State     SessionState `json:"state"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   *time.Time   `json:"ended_at,omitempty"`
	Output    []string     `json:"output"`
	Error     string       `json:"error,omitempty"`
	mu        sync.Mutex
}

// Manager manages Claude Code sessions
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

// CreateSession creates a new Claude Code session
func (m *Manager) CreateSession(projectPath string) (*Session, error) {
	// Verify project exists
	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("project path does not exist: %s", projectPath)
	}

	session := &Session{
		ID:        generateSessionID(),
		Project:   projectPath,
		State:     SessionIdle,
		StartedAt: time.Now(),
		Output:    []string{},
	}

	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()

	return session, nil
}

// StartSession starts a Claude Code session
func (m *Manager) StartSession(sessionID string, task string, timeoutSeconds int) error {
	session := m.GetSession(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	session.State = SessionSpawning
	session.mu.Unlock()

	// Spawn Claude Code in background
	go m.runSession(session, task, timeoutSeconds)

	return nil
}

// runSession runs the Claude Code session
func (m *Manager) runSession(session *Session, task string, timeoutSeconds int) {
	session.mu.Lock()
	session.State = SessionRunning
	session.mu.Unlock()

	// Build Claude Code command
	args := []string{
		"code",
		"--print",
		"--silent",
		"--dangerously-skip-permissions",
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = session.Project

	// Set up environment
	env := os.Environ()
	env = append(env, "CLAUDE_SDK_LOG=error")
	env = append(env, "CLAUDE_NO_ANALYTICS=true")
	cmd.Env = env

	// Set timeout
	done := make(chan struct{})

	go func() {
		// Get output
		output, oerr := cmd.CombinedOutput()
		if oerr != nil {
			session.mu.Lock()
			session.Error = oerr.Error()
			session.State = SessionError
			session.mu.Unlock()
		} else {
			session.mu.Lock()
			session.Output = append(session.Output, string(output))
			session.State = SessionDone
			session.mu.Unlock()
		}
		close(done)
	}()

	// Wait for completion or timeout
	select {
	case <-done:
		// Completed
	case <-time.After(time.Duration(timeoutSeconds) * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		session.mu.Lock()
		session.State = SessionError
		session.Error = "timeout"
		session.mu.Unlock()
	}

	now := time.Now()
	session.mu.Lock()
	session.EndedAt = &now
	session.mu.Unlock()
}

// GetSession retrieves a session by ID
func (m *Manager) GetSession(sessionID string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sessionID]
}

// ListSessions returns all sessions
func (m *Manager) ListSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	return sessions
}

// GetActiveSessions returns sessions that are not done or errored
func (m *Manager) GetActiveSessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var active []*Session
	for _, s := range m.sessions {
		if s.State == SessionRunning || s.State == SessionWaiting || s.State == SessionSpawning {
			active = append(active, s)
		}
	}
	return active
}

// CloseSession cleans up a session
func (m *Manager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists := m.sessions[sessionID]; exists {
		session.mu.Lock()
		session.State = SessionDone
		now := time.Now()
		session.EndedAt = &now
		session.mu.Unlock()
		delete(m.sessions, sessionID)
	}
	return nil
}

// SessionToJSON converts a session to JSON
func SessionToJSON(session *Session) (string, error) {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// generateSessionID creates a unique session ID
func generateSessionID() string {
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}

// ReadSessionOutput reads output from a running session
func ReadSessionOutput(session *Session) ([]string, error) {
	session.mu.Lock()
	defer session.mu.Unlock()

	// Return current output
	return session.Output, nil
}

// IsSessionDone checks if a session has completed
func IsSessionDone(session *Session) bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.State == SessionDone || session.State == SessionError
}

// GetSessionStatus returns a human-readable status
func GetSessionStatus(session *Session) string {
	session.mu.Lock()
	defer session.mu.Unlock()

	switch session.State {
	case SessionIdle:
		return "Idle"
	case SessionSpawning:
		return "Starting..."
	case SessionRunning:
		return fmt.Sprintf("Running (started %s)", session.StartedAt.Format("15:04:05"))
	case SessionWaiting:
		return "Waiting for input"
	case SessionDone:
		if session.Error != "" {
			return fmt.Sprintf("Error: %s", session.Error)
		}
		return "Completed"
	case SessionError:
		return fmt.Sprintf("Error: %s", session.Error)
	default:
		return "Unknown"
	}
}
