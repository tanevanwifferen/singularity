package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/git"
)

// handleWebSocket upgrades the HTTP connection and starts the per-connection
// reader / heartbeat goroutines.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	client := &wsClient{conn: conn}

	s.wsMux.Lock()
	s.wsClients[conn] = client
	s.wsMux.Unlock()

	defer func() {
		s.wsMux.Lock()
		delete(s.wsClients, conn)
		s.wsMux.Unlock()
		// Drop the conn from every stream's subscriber set. Streams whose
		// last subscriber just vanished are canceled so their pollers don't
		// tick until daemon shutdown.
		s.streamMu.Lock()
		var abandoned []string
		for id, e := range s.streams {
			e.mu.Lock()
			if e.subscribers[conn] {
				delete(e.subscribers, conn)
				if len(e.subscribers) == 0 {
					abandoned = append(abandoned, id)
				}
			}
			e.mu.Unlock()
		}
		s.streamMu.Unlock()
		for _, id := range abandoned {
			s.cancelStream(id)
		}
		conn.Close()
	}()

	go s.wsHeartbeat(client)
	s.wsReader(client)
}

// wsHeartbeat sends periodic pings to keep the connection alive.
func (s *Server) wsHeartbeat(c *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := c.writeControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

// wsPongWait bounds how long a connection may go without any inbound
// traffic (message or pong). The heartbeat pings every 30s, so a healthy
// peer always answers well within this window; a dead one gets reaped
// instead of silently pinning resources forever.
const wsPongWait = 90 * time.Second

// wsReader reads incoming WS messages and dispatches them.
func (s *Server) wsReader(c *wsClient) {
	c.conn.SetReadLimit(maxBodyBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		var wsMsg api.WSMessage
		if err := json.Unmarshal(msg, &wsMsg); err != nil {
			s.wsSendError(c, "invalid message format", api.ErrCodeBadRequest)
			continue
		}
		s.handleWSMessage(c, &wsMsg)
	}
}

// handleWSMessage routes client-to-server WS messages.
func (s *Server) handleWSMessage(c *wsClient, msg *api.WSMessage) {
	switch msg.Type {
	case api.WSMsgSubscribe:
		s.wsSend(c, api.WSMessage{Type: api.WSEventSubscribed, Payload: api.SubscribedPayload{Status: "ok"}})

	case api.WSMsgRefreshRepo:
		path := s.getRepoPath()
		if p, ok := payloadString(msg.Payload, "path"); ok && p != "" {
			path = p
		}
		if path != "" {
			if info, err := git.OpenRepo(path); err == nil {
				s.wsBroadcast(api.WSMessage{Type: api.WSEventRepoUpdate, Payload: info})
			}
		}

	case api.WSMsgSubscribeStream:
		id, _ := payloadString(msg.Payload, "stream_id")
		if id == "" {
			s.wsSendError(c, "missing stream_id", api.ErrCodeBadRequest)
			return
		}
		s.streamMu.Lock()
		e, ok := s.streams[id]
		s.streamMu.Unlock()
		if !ok {
			s.wsSendError(c, "unknown stream_id: "+id, api.ErrCodeNotFound)
			// Also send a stream-scoped terminal frame so a client-side
			// handler waiting on this id closes instead of hanging forever.
			s.wsSend(c, api.WSMessage{
				Type:    api.WSStreamPrefix + id,
				Payload: api.StreamFrame{StreamID: id, Done: true, Error: "unknown stream_id: " + id, Timestamp: time.Now().UTC()},
			})
			return
		}
		s.wsSend(c, api.WSMessage{Type: api.WSEventSubscribed, Payload: api.SubscribedPayload{Status: "ok"}})
		// Attach and replay any frames broadcast before the first subscriber
		// arrived. The replay happens under e.mu so a concurrent broadcast
		// can't interleave newer frames before the buffered ones.
		e.mu.Lock()
		e.subscribers[c.conn] = true
		e.hasSubscriber = true
		pending := e.pending
		e.pending = nil
		for _, data := range pending {
			if err := c.writeMessage(websocket.TextMessage, data); err != nil {
				log.Printf("Error replaying stream frame: %v", err)
				break
			}
		}
		e.mu.Unlock()

	case api.WSMsgCancelStream:
		id, _ := payloadString(msg.Payload, "stream_id")
		if id != "" {
			s.cancelStream(id)
		}

	default:
		s.wsSendError(c, fmt.Sprintf("unknown message type: %s", msg.Type), api.ErrCodeBadRequest)
	}
}

// payloadString extracts a string field from a JSON-decoded payload (which
// arrives as map[string]interface{} from encoding/json).
func payloadString(p interface{}, key string) (string, bool) {
	m, ok := p.(map[string]interface{})
	if !ok {
		return "", false
	}
	v, ok := m[key].(string)
	return v, ok
}

// wsSend marshals and writes one message to a single connection. All writes
// go through wsClient.writeMessage so the per-conn write mutex serializes
// concurrent writers (heartbeat + broadcast + stream pump).
func (s *Server) wsSend(c *wsClient, msg api.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling WS message: %v", err)
		return
	}
	if err := c.writeMessage(websocket.TextMessage, data); err != nil {
		log.Printf("Error sending WS message: %v", err)
	}
}

// wsSendError sends an "error" WS frame with an optional stable code.
func (s *Server) wsSendError(c *wsClient, errMsg, code string) {
	s.wsSend(c, api.WSMessage{Type: api.WSEventError, Payload: api.ErrorPayload{Error: errMsg, Code: code}})
}

// wsBroadcast sends one message to every connected client. Each write is
// serialized per-connection by wsClient.writeMessage.
func (s *Server) wsBroadcast(msg api.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling WS broadcast: %v", err)
		return
	}
	// Snapshot the client set so we don't hold wsMux while writing.
	s.wsMux.RLock()
	clients := make([]*wsClient, 0, len(s.wsClients))
	for _, c := range s.wsClients {
		clients = append(clients, c)
	}
	s.wsMux.RUnlock()
	for _, c := range clients {
		if err := c.writeMessage(websocket.TextMessage, data); err != nil {
			log.Printf("Error broadcasting to client: %v", err)
		}
	}
}

// BroadcastRepoUpdate broadcasts a repo update event.
func (s *Server) BroadcastRepoUpdate(repo *git.RepoInfo) {
	s.wsBroadcast(api.WSMessage{Type: api.WSEventRepoUpdate, Payload: repo})
}

// BroadcastBranchUpdate broadcasts a branch update event.
func (s *Server) BroadcastBranchUpdate(branch string) {
	s.wsBroadcast(api.WSMessage{
		Type:    api.WSEventBranchUpdate,
		Payload: api.BranchUpdatePayload{Branch: branch},
	})
}
