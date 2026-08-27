// Package client is the HTTP+WebSocket SDK that lets the TUI talk to the
// singularity daemon. It mirrors the wire contract in
// docs/design/WIRE-CONTRACT.md one-to-one: every service.Services method is
// exposed as a Client.<Capability><Method> in this package.
//
// Server-side errors carry a stable Code; mapError translates that back into
// a sentinel from internal/service so views can use errors.Is end-to-end.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// Client is the API client for connecting to the singularity daemon.
type Client struct {
	// serverURL is the base URL used for HTTP requests. For unix-socket
	// endpoints this is the sentinel "http://unix"; the underlying
	// Transport is configured to ignore the host and dial the socket.
	serverURL string

	// sockPath is the AF_UNIX socket path when serverURL is the unix
	// sentinel; empty for TCP endpoints. The WS dialer uses this to
	// install its own NetDialContext.
	sockPath string

	httpClient *http.Client

	wsMux    sync.RWMutex
	wsConn   *websocket.Conn
	onUpdate func(event *api.WSMessage)

	// wsWriteMu serializes writes to wsConn. gorilla/websocket forbids
	// concurrent writers on one Conn; every write goes through SendWSMessage
	// which takes this mutex.
	wsWriteMu sync.Mutex

	// streamMux guards the streamHandlers map (one handler per active
	// stream:<id> subscription).
	streamMux      sync.RWMutex
	streamHandlers map[string]func(api.StreamFrame)

	// authToken, when non-empty, is sent on every HTTP and WS request as
	// `Authorization: Bearer <token>`. Set via SetAuthToken; remains empty
	// for unix-socket clients where the daemon does not require auth.
	authToken string
}

// SetAuthToken configures the bearer token sent on every HTTP and WS
// request. Empty disables the header (the unix-socket default).
func (c *Client) SetAuthToken(token string) {
	c.authToken = token
}

// NewClient creates a new API client. The serverURL may be:
//
//	http://host:port           — plain TCP
//	https://host:port          — TLS over TCP
//	unix:///abs/path/sock      — HTTP over an AF_UNIX socket
//
// For the unix case the returned client transparently rewrites requests
// to "http://unix/<path>" and dials the socket; callers don't need to
// know which transport is in use.
func NewClient(serverURL string) *Client {
	trimmed := strings.TrimSuffix(serverURL, "/")
	c := &Client{
		streamHandlers: make(map[string]func(api.StreamFrame)),
	}
	if u, err := url.Parse(trimmed); err == nil && u.Scheme == "unix" {
		sock := u.Path
		c.sockPath = sock
		c.serverURL = "http://unix"
		c.httpClient = &http.Client{
			Timeout: httpBackstopTimeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sock)
				},
			},
		}
		return c
	}
	c.serverURL = trimmed
	c.httpClient = &http.Client{Timeout: httpBackstopTimeout}
	return c
}

// httpBackstopTimeout is a safety net against a wedged daemon, deliberately
// far above any legitimate request duration. Callers control real deadlines
// via per-request contexts (LLM-backed endpoints legitimately take minutes,
// so a tight transport-level timeout would silently override those).
const httpBackstopTimeout = 10 * time.Minute

// SetUpdateHandler sets the catch-all callback invoked for every non-stream
// WS event. Existing client of the SDK; retained for backward compat.
func (c *Client) SetUpdateHandler(handler func(event *api.WSMessage)) {
	c.onUpdate = handler
}

// Connect establishes a WebSocket connection.
func (c *Client) Connect() error {
	wsURL := strings.Replace(c.serverURL, "http://", "ws://", 1) + "/ws"
	if strings.HasPrefix(c.serverURL, "https://") {
		wsURL = strings.Replace(c.serverURL, "https://", "wss://", 1) + "/ws"
	}

	dialer := *websocket.DefaultDialer
	if c.sockPath != "" {
		sock := c.sockPath
		dialer.NetDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
	}
	var hdr http.Header
	if c.authToken != "" {
		hdr = http.Header{"Authorization": []string{"Bearer " + c.authToken}}
	}
	conn, _, err := dialer.Dial(wsURL, hdr)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	c.wsMux.Lock()
	c.wsConn = conn
	c.wsMux.Unlock()

	go c.wsReader()
	return nil
}

// Disconnect closes the WebSocket connection.
func (c *Client) Disconnect() error {
	c.wsMux.Lock()
	defer c.wsMux.Unlock()
	if c.wsConn != nil {
		return c.wsConn.Close()
	}
	return nil
}

// wsReader reads WebSocket messages.
func (c *Client) wsReader() {
	for {
		c.wsMux.RLock()
		conn := c.wsConn
		c.wsMux.RUnlock()
		if conn == nil {
			c.failStreams("connection closed")
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			// The connection is gone (daemon restart, network drop, or a
			// local Disconnect). Terminate every in-flight stream so
			// consumers blocked on their channels unwedge instead of
			// hanging forever.
			c.failStreams(fmt.Sprintf("connection lost: %v", err))
			return
		}

		var event api.WSMessage
		if err := json.Unmarshal(msg, &event); err != nil {
			continue
		}

		// Stream frames carry a "stream:<id>" type. Route them to the
		// per-stream handler registered by Client.subscribeStream.
		if strings.HasPrefix(event.Type, api.WSStreamPrefix) {
			id := strings.TrimPrefix(event.Type, api.WSStreamPrefix)
			c.streamMux.RLock()
			h := c.streamHandlers[id]
			c.streamMux.RUnlock()
			if h != nil {
				frame := decodeStreamFrame(event.Payload)
				h(frame)
			}
			continue
		}

		if c.onUpdate != nil {
			c.onUpdate(&event)
		}
	}
}

// failStreams delivers a synthetic terminal frame to every registered stream
// handler and clears the registry. Called when the WS connection dies so
// every consumer sees an error event followed by channel close.
func (c *Client) failStreams(reason string) {
	c.streamMux.Lock()
	handlers := c.streamHandlers
	c.streamHandlers = make(map[string]func(api.StreamFrame))
	c.streamMux.Unlock()
	for id, h := range handlers {
		h(api.StreamFrame{StreamID: id, Done: true, Error: reason})
	}
}

// decodeStreamFrame re-marshals payload (which encoding/json leaves as
// map[string]interface{}) back into an api.StreamFrame.
func decodeStreamFrame(payload interface{}) api.StreamFrame {
	data, err := json.Marshal(payload)
	if err != nil {
		return api.StreamFrame{}
	}
	var frame api.StreamFrame
	_ = json.Unmarshal(data, &frame)
	return frame
}

// SendWSMessage sends a WebSocket message. Writes are serialized under
// wsWriteMu — gorilla/websocket panics on concurrent writes to one Conn
// (e.g. multiple goroutines each starting a stream subscription).
func (c *Client) SendWSMessage(msgType string, payload interface{}) error {
	c.wsMux.RLock()
	conn := c.wsConn
	c.wsMux.RUnlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	msg := api.WSMessage{Type: msgType, Payload: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wsWriteMu.Lock()
	defer c.wsWriteMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

// Subscribe subscribes to server events.
func (c *Client) Subscribe() error {
	return c.SendWSMessage(api.WSMsgSubscribe, nil)
}

// RefreshRepo requests a repo refresh.
func (c *Client) RefreshRepo() error {
	return c.SendWSMessage(api.WSMsgRefreshRepo, nil)
}

// ---------------- HTTP plumbing ----------------

// doRequest performs an HTTP request and decodes the api.APIResponse envelope
// into result. Non-2xx responses are mapped to a service sentinel via
// mapError using the Code field.
func (c *Client) doRequest(ctx context.Context, method, path string, body, result interface{}) error {
	url := c.serverURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return service.ErrCanceled
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var apiResp api.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP error: %d", resp.StatusCode)
		}
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !apiResp.Success {
		return mapError(apiResp.Code, apiResp.Error)
	}

	if result != nil && apiResp.Data != nil {
		data, err := json.Marshal(apiResp.Data)
		if err != nil {
			return fmt.Errorf("failed to marshal data: %w", err)
		}
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}
	return nil
}

// mapError translates a server-sent (code, message) pair back into the
// matching service sentinel. Unknown codes wrap a generic error that still
// preserves the message for human display.
func mapError(code, msg string) error {
	if msg == "" {
		msg = code
	}
	switch code {
	case api.ErrCodeMRAlreadyExists:
		return fmt.Errorf("%s: %w", msg, service.ErrMRAlreadyExists)
	case api.ErrCodeNotFound:
		return fmt.Errorf("%s: %w", msg, service.ErrNotFound)
	case api.ErrCodeConflict:
		return fmt.Errorf("%s: %w", msg, service.ErrConflict)
	case api.ErrCodeAgentLimit:
		return fmt.Errorf("%s: %w", msg, service.ErrAgentLimit)
	case api.ErrCodeNoForge:
		return fmt.Errorf("%s: %w", msg, service.ErrNoForge)
	case api.ErrCodeRebaseInProgress:
		return fmt.Errorf("%s: %w", msg, service.ErrRebaseInProgress)
	case api.ErrCodeNoRebaseInProgress:
		return fmt.Errorf("%s: %w", msg, service.ErrNoRebaseInProgress)
	case api.ErrCodePermissionDenied:
		return fmt.Errorf("%s: %w", msg, service.ErrPermissionDenied)
	case api.ErrCodeUnavailable:
		return fmt.Errorf("%s: %w", msg, service.ErrUnavailable)
	case api.ErrCodeCanceled:
		return fmt.Errorf("%s: %w", msg, service.ErrCanceled)
	}
	if msg == "" {
		return errors.New("unknown error")
	}
	return errors.New(msg)
}

// post is a tiny helper for the common-case POST + decode pattern. result
// may be nil for endpoints that don't return data.
func (c *Client) post(ctx context.Context, path string, body, result interface{}) error {
	return c.doRequest(ctx, http.MethodPost, path, body, result)
}

// get is a tiny helper for the common-case GET + decode pattern.
func (c *Client) get(ctx context.Context, path string, result interface{}) error {
	return c.doRequest(ctx, http.MethodGet, path, nil, result)
}

// GetStatus returns the daemon status. Retained for daemon-lifecycle checks
// outside the service interface.
func (c *Client) GetStatus(ctx context.Context) (*api.StatusResponse, error) {
	var resp api.StatusResponse
	if err := c.get(ctx, "/api/status", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
