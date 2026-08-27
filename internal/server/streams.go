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
	// Reap streams nobody ever subscribed to (client died between the POST
	// and its subscribe_stream); otherwise their pollers tick forever.
	time.AfterFunc(subscribeGrace, func() {
		entry.mu.Lock()
		orphaned := !entry.hasSubscriber
		entry.mu.Unlock()
		if orphaned {
			s.cancelStream(id)
		}
	})
	return id
}

// streamRetention is how long a finished stream's entry (and its buffered
// frames) stays in the registry. A subscribe_stream that races the terminal
// frame within this window still gets the full replay instead of an
// unknown-stream error.
const streamRetention = 30 * time.Second

// subscribeGrace is how long a stream may exist without any subscriber ever
// attaching before it is reaped. Protects a long-lived daemon against
// clients that POSTed a stream endpoint and then died before subscribing.
const subscribeGrace = 2 * time.Minute

// maxPendingFrames bounds the pre-subscribe replay buffer per stream.
const maxPendingFrames = 4096

// cancelStream invokes the underlying cancel closure. The registry entry is
// NOT removed here: cancellation makes the service close its channel, after
// which pumpStream broadcasts the terminal frame (so subscribers see a Done
// instead of a silent stall) and schedules the entry's removal. Idempotent.
func (s *Server) cancelStream(id string) {
	s.streamMu.Lock()
	e, ok := s.streams[id]
	s.streamMu.Unlock()
	if ok && e.cancel != nil {
		e.cancel()
	}
}

// finishStream runs when a stream's pump exits: it fires the cancel closure
// (idempotent — tears down the service subscription if still live) and
// schedules the registry entry's removal after streamRetention.
func (s *Server) finishStream(id string) {
	s.streamMu.Lock()
	e, ok := s.streams[id]
	s.streamMu.Unlock()
	if !ok {
		return
	}
	if e.cancel != nil {
		e.cancel()
	}
	time.AfterFunc(streamRetention, func() {
		s.streamMu.Lock()
		delete(s.streams, id)
		s.streamMu.Unlock()
	})
}

// pumpStream reads frames from src and forwards each one to every WS client
// subscribed to streamID. When src closes (or a terminal frame is emitted)
// the entry is retained for streamRetention, then dropped. Run in a goroutine.
//
// frameOf is invoked per channel value to construct the wire frame; this
// abstracts away the concrete event type so one pump works for AgentEvent,
// SyncProgressEvent, etc.
func pumpStream[T any](s *Server, streamID string, src <-chan T, frameOf func(T) (api.StreamFrame, bool)) {
	defer s.finishStream(streamID)
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
	// while writing. Until the first subscriber attaches, frames are
	// buffered on the entry and replayed on subscribe — the pump starts
	// before the client's subscribe_stream can possibly arrive.
	e.mu.Lock()
	if !e.hasSubscriber {
		if len(e.pending) < maxPendingFrames {
			e.pending = append(e.pending, data)
		}
		e.mu.Unlock()
		return
	}
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
