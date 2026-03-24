package app

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"singularity/internal/api"
	"singularity/internal/git"

	"github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// WSClient represents a WebSocket client for receiving server events
type WSClient struct {
	// Connection state
	conn       *websocket.Conn
	url        string
	connected  bool
	connecting bool
	mu         sync.RWMutex

	// Reconnection state
	reconnectDelay    time.Duration
	maxReconnectDelay time.Duration
	reconnectAttempts  int
	maxReconnectAttempts int
	stopCh             chan struct{}

	// Event handlers - each view type registers its handler
	handlers map[string][]WSEventHandler

	// Subscriber for connection status changes
	statusSubscribers []chan WSConnectionStatus
	subMu             sync.RWMutex
}

// WSConnectionStatus represents the current connection state
type WSConnectionStatus struct {
	Connected    bool
	URL          string
	Error        string
	Reconnecting bool
}

// WSEventHandler is a callback function for handling WebSocket events
type WSEventHandler func(eventType string, payload json.RawMessage) tea.Cmd

// WSEvent represents a parsed WebSocket event with typed payload
type WSEvent struct {
	Type    string
	Payload json.RawMessage
}

// NewWSClient creates a new WebSocket client
func NewWSClient(url string) *WSClient {
	return &WSClient{
		url:                 url,
		reconnectDelay:      1 * time.Second,
		maxReconnectDelay:   30 * time.Second,
		maxReconnectAttempts: 0, // 0 = unlimited
		stopCh:              make(chan struct{}),
		handlers:            make(map[string][]WSEventHandler),
		statusSubscribers:   make([]chan WSConnectionStatus, 0),
	}
}

// Connect establishes the WebSocket connection
func (c *WSClient) Connect() error {
	c.mu.Lock()
	if c.connecting || c.connected {
		c.mu.Unlock()
		return nil
	}
	c.connecting = true
	c.mu.Unlock()

	c.notifyStatus(WSConnectionStatus{Connected: false, URL: c.url, Reconnecting: c.reconnectAttempts > 0})

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}

	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		c.mu.Lock()
		c.connecting = false
		c.mu.Unlock()
		c.notifyStatus(WSConnectionStatus{Connected: false, URL: c.url, Error: err.Error(), Reconnecting: c.reconnectAttempts > 0})
		return fmt.Errorf("WebSocket dial error: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.connecting = false
	c.mu.Unlock()

	c.notifyStatus(WSConnectionStatus{Connected: true, URL: c.url, Reconnecting: false})

	// Subscribe to events
	c.subscribe()

	// Start reader
	go c.readLoop()

	log.Printf("[WS] Connected to %s", c.url)
	return nil
}

// subscribe sends a subscription message to the server
func (c *WSClient) subscribe() {
	c.mu.RLock()
	if c.conn == nil {
		c.mu.RUnlock()
		return
	}
	conn := c.conn
	c.mu.RUnlock()

	msg := api.WSMessage{
		Type:    "subscribe",
		Payload: map[string]string{},
	}
	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("[WS] Subscribe error: %v", err)
	}
}

// Disconnect closes the WebSocket connection
func (c *WSClient) Disconnect() error {
	close(c.stopCh)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.connected = false
		return err
	}
	return nil
}

// readLoop continuously reads messages from the WebSocket
func (c *WSClient) readLoop() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		c.mu.RLock()
		if c.conn == nil {
			c.mu.RUnlock()
			return
		}
		conn := c.conn
		c.mu.RUnlock()

		// Set read deadline
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WS] Read error: %v", err)
			}
			c.handleDisconnect()
			return
		}

		c.handleMessage(msg)
	}
}

// handleMessage processes an incoming WebSocket message
func (c *WSClient) handleMessage(msg []byte) {
	var wsMsg api.WSMessage
	if err := json.Unmarshal(msg, &wsMsg); err != nil {
		log.Printf("[WS] Parse error: %v", err)
		return
	}

	// Convert payload to json.RawMessage if needed
	var payload json.RawMessage
	if wsMsg.Payload != nil {
		if raw, ok := wsMsg.Payload.(json.RawMessage); ok {
			payload = raw
		} else {
			// Marshal back to JSON if it's another type
			data, err := json.Marshal(wsMsg.Payload)
			if err != nil {
				log.Printf("[WS] Payload marshal error: %v", err)
				return
			}
			payload = data
		}
	}

	// Dispatch to registered handlers
	c.dispatch(wsMsg.Type, payload)
}

// dispatch sends an event to all registered handlers for that event type
func (c *WSClient) dispatch(eventType string, payload json.RawMessage) {
	c.mu.RLock()
	handlers := c.handlers[eventType]
	// Also get handlers registered for "*" (all events)
	allHandlers := c.handlers["*"]
	c.mu.RUnlock()

	for _, h := range handlers {
		h(eventType, payload)
	}
	for _, h := range allHandlers {
		h(eventType, payload)
	}
}

// handleDisconnect handles connection loss and triggers reconnection
func (c *WSClient) handleDisconnect() {
	c.mu.Lock()
	c.connected = false
	if c.conn != nil {
		c.conn = nil
	}
	c.mu.Unlock()

	c.notifyStatus(WSConnectionStatus{Connected: false, URL: c.url, Reconnecting: true})

	// Check if we should reconnect
	if c.maxReconnectAttempts > 0 && c.reconnectAttempts >= c.maxReconnectAttempts {
		log.Printf("[WS] Max reconnect attempts reached (%d)", c.maxReconnectAttempts)
		c.notifyStatus(WSConnectionStatus{Connected: false, URL: c.url, Error: "max reconnect attempts reached", Reconnecting: false})
		return
	}

	// Exponential backoff
	c.reconnectAttempts++
	delay := c.reconnectDelay * time.Duration(c.reconnectAttempts)
	if delay > c.maxReconnectDelay {
		delay = c.maxReconnectDelay
	}

	log.Printf("[WS] Reconnecting in %v (attempt %d)", delay, c.reconnectAttempts)

	// Wait and reconnect
	timer := time.NewTimer(delay)
	select {
	case <-c.stopCh:
		timer.Stop()
		return
	case <-timer.C:
	}

	// Reset stopCh for new connection
	c.stopCh = make(chan struct{})

	if err := c.Connect(); err != nil {
		log.Printf("[WS] Reconnect failed: %v", err)
		// handleDisconnect will be called again from readLoop failure
	}
}

// RegisterHandler registers a handler for a specific event type
// Event types: repo_update, branch_update, pipeline_update, agent_output, agent_started, agent_complete, agent_error, project_update
func (c *WSClient) RegisterHandler(eventType string, handler WSEventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[eventType] = append(c.handlers[eventType], handler)
}

// RegisterAllHandler registers a handler for all event types
func (c *WSClient) RegisterAllHandler(handler WSEventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers["*"] = append(c.handlers["*"], handler)
}

// SubscribeStatus registers a channel to receive connection status updates
func (c *WSClient) SubscribeStatus(ch chan WSConnectionStatus) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.statusSubscribers = append(c.statusSubscribers, ch)
}

// UnsubscribeStatus removes a status subscriber channel
func (c *WSClient) UnsubscribeStatus(ch chan WSConnectionStatus) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for i, sub := range c.statusSubscribers {
		if sub == ch {
			c.statusSubscribers = append(c.statusSubscribers[:i], c.statusSubscribers[i+1:]...)
			return
		}
	}
}

// notifyStatus sends status updates to all subscribers
func (c *WSClient) notifyStatus(status WSConnectionStatus) {
	c.subMu.RLock()
	subs := make([]chan WSConnectionStatus, len(c.statusSubscribers))
	copy(subs, c.statusSubscribers)
	c.subMu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- status:
		default:
			// Channel full, skip
		}
	}
}

// IsConnected returns the current connection state
func (c *WSClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// ReconnectAttempts returns the current number of reconnection attempts
func (c *WSClient) ReconnectAttempts() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reconnectAttempts
}

// SetMaxReconnectAttempts sets the maximum number of reconnection attempts (0 = unlimited)
func (c *WSClient) SetMaxReconnectAttempts(max int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxReconnectAttempts = max
}

// WSConnectionMsg is a tea.Msg for connection status updates
type WSConnectionMsg struct {
	Status WSConnectionStatus
}

// WSRepoUpdateMsg is a tea.Msg for repository update events
type WSRepoUpdateMsg struct {
	Repo *git.RepoInfo
}

// WSBranchUpdateMsg is a tea.Msg for branch update events
type WSBranchUpdateMsg struct {
	Branch string
}

// WSPipelineUpdateMsg is a tea.Msg for pipeline update events
type WSPipelineUpdateMsg struct {
	Branch string
}

// WSAgentOutputMsg is a tea.Msg for agent output events
type WSAgentOutputMsg struct {
	AgentID  string
	Output   string
	Source   string
	Timestamp time.Time
}

// WSAgentEventMsg is a tea.Msg for agent lifecycle events (started, complete, error)
type WSAgentEventMsg struct {
	AgentID   string
	EventType string // agent_started, agent_complete, agent_error
	Error     string
}

// WSProjectUpdateMsg is a tea.Msg for project update events
type WSProjectUpdateMsg struct {
	Status string
}

// NewWSViewUpdater creates a WebSocket client with standard view update handlers
func NewWSViewUpdater(url string, repoPath string) *WSClient {
	client := NewWSClient(url)

	// Register handler for repo updates
	client.RegisterHandler("repo_update", func(eventType string, payload json.RawMessage) tea.Cmd {
		var repo git.RepoInfo
		if err := json.Unmarshal(payload, &repo); err != nil {
			log.Printf("[WS] Failed to parse repo_update: %v", err)
			return nil
		}
		return func() tea.Msg {
			return WSRepoUpdateMsg{Repo: &repo}
		}
	})

	// Register handler for branch updates
	client.RegisterHandler("branch_update", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data map[string]string
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse branch_update: %v", err)
			return nil
		}
		branch := data["branch"]
		return func() tea.Msg {
			return WSBranchUpdateMsg{Branch: branch}
		}
	})

	// Register handler for pipeline updates
	client.RegisterHandler("pipeline_update", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data map[string]string
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse pipeline_update: %v", err)
			return nil
		}
		branch := data["branch"]
		return func() tea.Msg {
			return WSPipelineUpdateMsg{Branch: branch}
		}
	})

	// Register handler for agent output
	client.RegisterHandler("agent_output", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data struct {
			AgentID   string `json:"agent_id"`
			Output    string `json:"output"`
			Source    string `json:"source"`
			Timestamp string `json:"timestamp"`
		}
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse agent_output: %v", err)
			return nil
		}
		ts := time.Now()
		if data.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339, data.Timestamp); err == nil {
				ts = t
			}
		}
		return func() tea.Msg {
			return WSAgentOutputMsg{
				AgentID:  data.AgentID,
				Output:   data.Output,
				Source:   data.Source,
				Timestamp: ts,
			}
		}
	})

	// Register handler for agent started
	client.RegisterHandler("agent_started", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse agent_started: %v", err)
			return nil
		}
		return func() tea.Msg {
			return WSAgentEventMsg{
				AgentID:   data.AgentID,
				EventType: "agent_started",
			}
		}
	})

	// Register handler for agent complete
	client.RegisterHandler("agent_complete", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse agent_complete: %v", err)
			return nil
		}
		return func() tea.Msg {
			return WSAgentEventMsg{
				AgentID:   data.AgentID,
				EventType: "agent_complete",
			}
		}
	})

	// Register handler for agent error
	client.RegisterHandler("agent_error", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data struct {
			AgentID string `json:"agent_id"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse agent_error: %v", err)
			return nil
		}
		return func() tea.Msg {
			return WSAgentEventMsg{
				AgentID:   data.AgentID,
				EventType: "agent_error",
				Error:     data.Error,
			}
		}
	})

	// Register handler for project updates
	client.RegisterHandler("project_update", func(eventType string, payload json.RawMessage) tea.Cmd {
		var data struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(payload, &data); err != nil {
			log.Printf("[WS] Failed to parse project_update: %v", err)
			return nil
		}
		return func() tea.Msg {
			return WSProjectUpdateMsg{Status: data.Status}
		}
	})

	return client
}
