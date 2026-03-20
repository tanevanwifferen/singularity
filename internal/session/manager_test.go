package session

import (
	"os"
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if len(m.ListSessions()) != 0 {
		t.Errorf("new manager should have 0 sessions, got %d", len(m.ListSessions()))
	}
}

func TestCreateSession(t *testing.T) {
	m := NewManager()
	s, err := m.CreateSession(os.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.ID == "" {
		t.Fatal("session ID should not be empty")
	}
	if s.State != SessionIdle {
		t.Errorf("expected state idle, got %s", s.State)
	}
	if s.Project != os.TempDir() {
		t.Errorf("expected project %q, got %q", os.TempDir(), s.Project)
	}
}

func TestCreateSessionInvalidPath(t *testing.T) {
	m := NewManager()
	_, err := m.CreateSession("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestGetSession(t *testing.T) {
	m := NewManager()
	s, _ := m.CreateSession(os.TempDir())

	got := m.GetSession(s.ID)
	if got == nil {
		t.Fatal("expected to find session")
	}
	if got.ID != s.ID {
		t.Errorf("got different session ID: %s vs %s", got.ID, s.ID)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	m := NewManager()
	got := m.GetSession("nonexistent")
	if got != nil {
		t.Fatal("expected nil for nonexistent session")
	}
}

func TestListSessions(t *testing.T) {
	m := NewManager()
	m.CreateSession(os.TempDir())
	m.CreateSession(os.TempDir())

	sessions := m.ListSessions()
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestGetActiveSessions(t *testing.T) {
	m := NewManager()
	s1, _ := m.CreateSession(os.TempDir())
	s2, _ := m.CreateSession(os.TempDir())

	// s1 is idle, s2 set to running
	s2.mu.Lock()
	s2.State = SessionRunning
	s2.mu.Unlock()

	active := m.GetActiveSessions()
	if len(active) != 1 {
		t.Errorf("expected 1 active session, got %d", len(active))
	}
	if len(active) > 0 && active[0].ID != s2.ID {
		t.Errorf("expected active session %s, got %s", s2.ID, active[0].ID)
	}
	_ = s1 // s1 is idle, should not appear in active
}

func TestCloseSession(t *testing.T) {
	m := NewManager()
	s, _ := m.CreateSession(os.TempDir())

	err := m.CloseSession(s.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.GetSession(s.ID) != nil {
		t.Error("session should be removed after close")
	}
}

func TestCloseSessionNotFound(t *testing.T) {
	m := NewManager()
	err := m.CloseSession("nonexistent")
	if err != nil {
		t.Fatal("CloseSession should not error for nonexistent session")
	}
}

func TestStartSessionNotFound(t *testing.T) {
	m := NewManager()
	err := m.StartSession("nonexistent", "task", 30)
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestSessionStateString(t *testing.T) {
	tests := []struct {
		state SessionState
		want  string
	}{
		{SessionIdle, "idle"},
		{SessionSpawning, "spawning"},
		{SessionRunning, "running"},
		{SessionWaiting, "waiting"},
		{SessionDone, "done"},
		{SessionError, "error"},
		{SessionState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("SessionState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestSessionToJSON(t *testing.T) {
	s := &Session{
		ID:      "test-123",
		Project: "/tmp",
		State:   SessionIdle,
		Output:  []string{},
	}
	jsonStr, err := SessionToJSON(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jsonStr == "" {
		t.Fatal("expected non-empty JSON")
	}
}

func TestIsSessionDone(t *testing.T) {
	s := &Session{State: SessionRunning}
	if IsSessionDone(s) {
		t.Error("running session should not be done")
	}
	s.State = SessionDone
	if !IsSessionDone(s) {
		t.Error("done session should be done")
	}
	s.State = SessionError
	if !IsSessionDone(s) {
		t.Error("error session should be done")
	}
}

func TestReadSessionOutput(t *testing.T) {
	s := &Session{
		Output: []string{"hello", "world"},
	}
	output, err := ReadSessionOutput(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(output) != 2 {
		t.Errorf("expected 2 output lines, got %d", len(output))
	}
}

func TestGetSessionStatus(t *testing.T) {
	tests := []struct {
		state SessionState
		err   string
		want  string
	}{
		{SessionIdle, "", "Idle"},
		{SessionSpawning, "", "Starting..."},
		{SessionWaiting, "", "Waiting for input"},
		{SessionDone, "", "Completed"},
		{SessionDone, "fail", "Error: fail"},
		{SessionError, "timeout", "Error: timeout"},
	}
	for _, tt := range tests {
		s := &Session{State: tt.state, Error: tt.err}
		got := GetSessionStatus(s)
		if got != tt.want {
			t.Errorf("GetSessionStatus(state=%s, err=%q) = %q, want %q", tt.state, tt.err, got, tt.want)
		}
	}
}

func TestGenerateSessionID(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateSessionID()
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}
