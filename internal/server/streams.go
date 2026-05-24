package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// streamContext returns a context whose lifetime is decoupled from any
// http.Request. Every *Subscribe handler MUST pass the returned ctx (not
// r.Context()) to the service layer so the subscription survives the HTTP
// handler returning. The returned combine() func produces a cancel closure
// that fires BOTH this context's cancel AND the service-supplied cancel; it
// is what registerStream stores so the cancel_stream WS message + Shutdown
// fully tear down the subscription.
//
// Background: the previous implementation passed r.Context() to Subscribe,
// which is canceled by net/http when the handler returns — milliseconds
// after the response is written. The poller goroutine exited immediately
// and the WS client never saw a single frame. See REVIEW.md P0.2.
func streamContext() (context.Context, func()) {
	return context.WithCancel(context.Background())
}

// combineCancel returns a cancel closure that invokes the daemon-side ctx
// cancel and the service-supplied cancel (if any). Idempotent.
func combineCancel(ctxCancel context.CancelFunc, serviceCancel func()) func() {
	return func() {
		if ctxCancel != nil {
			ctxCancel()
		}
		if serviceCancel != nil {
			serviceCancel()
		}
	}
}

// newStreamID mints an opaque stream identifier. Used by every **stream**
// HTTP endpoint to create a registry entry, return the ID in
// StreamStartResponse, and start the goroutine that pumps frames into the WS
// fan-out.
func newStreamID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a timestamp-based ID; collision odds remain negligible.
		return "s-" + hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000")))
	}
	return "s-" + hex.EncodeToString(b[:])
}

// registerStream registers a new stream with the given cancel closure (the
// closure returned by the service-layer Subscribe method). The returned ID
// is included in StreamStartResponse.
func (s *Server) registerStream(cancel func()) string {
	id := newStreamID()
	entry := &streamEntry{
		id:          id,
		cancel:      cancel,
		subscribers: make(map[*websocket.Conn]bool),
	}
	s.streamMu.Lock()
	s.streams[id] = entry
	s.streamMu.Unlock()
	return id
}

// cancelStream invokes the underlying cancel closure and removes the stream
// from the registry. Idempotent.
func (s *Server) cancelStream(id string) {
	s.streamMu.Lock()
	e, ok := s.streams[id]
	if ok {
		delete(s.streams, id)
	}
	s.streamMu.Unlock()
	if ok && e.cancel != nil {
		e.cancel()
	}
}

// pumpStream reads frames from src and forwards each one to every WS client
// subscribed to streamID. A terminal frame (Done=true) drops the entry from
// the registry. Run in a goroutine.
//
// frameOf is invoked per channel value to construct the wire frame; this
// abstracts away the concrete event type so one pump works for AgentEvent,
// SyncProgressEvent, etc.
func pumpStream[T any](s *Server, streamID string, src <-chan T, frameOf func(T) (api.StreamFrame, bool)) {
	defer s.cancelStream(streamID)
	for ev := range src {
		frame, done := frameOf(ev)
		frame.StreamID = streamID
		if frame.Timestamp.IsZero() {
			frame.Timestamp = time.Now().UTC()
		}
		s.broadcastStreamFrame(streamID, frame)
		if done {
			return
		}
	}
	// Channel closed without an explicit Done frame — send a terminal one
	// so clients know the stream ended cleanly.
	s.broadcastStreamFrame(streamID, api.StreamFrame{StreamID: streamID, Done: true, Timestamp: time.Now().UTC()})
}

// broadcastStreamFrame sends one stream frame to every connection currently
// subscribed to streamID. Connections not subscribed to this stream do not
// receive the frame. Per-connection writes go through wsClient.writeMessage
// so they cannot race the heartbeat / broadcast loops.
func (s *Server) broadcastStreamFrame(streamID string, frame api.StreamFrame) {
	s.streamMu.Lock()
	e, ok := s.streams[streamID]
	s.streamMu.Unlock()
	if !ok {
		return
	}
	msg := api.WSMessage{Type: api.WSStreamPrefix + streamID, Payload: frame}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error marshaling stream frame: %v", err)
		return
	}
	// Snapshot the subscriber set, then resolve each conn to its wsClient
	// wrapper outside the entry lock so we don't hold two locks at once
	// while writing.
	e.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(e.subscribers))
	for conn := range e.subscribers {
		conns = append(conns, conn)
	}
	e.mu.Unlock()
	s.wsMux.RLock()
	clients := make([]*wsClient, 0, len(conns))
	for _, conn := range conns {
		if c, ok := s.wsClients[conn]; ok {
			clients = append(clients, c)
		}
	}
	s.wsMux.RUnlock()
	for _, c := range clients {
		if err := c.writeMessage(websocket.TextMessage, data); err != nil {
			log.Printf("Error broadcasting stream frame: %v", err)
		}
	}
}
