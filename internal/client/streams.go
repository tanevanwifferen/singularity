package client

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// terminalSendTimeout bounds how long a terminal-frame delivery may block
// waiting for the consumer. Consumers are contractually required to drain
// the channel, but a consumer that broke out of its receive loop must not
// wedge the WS reader goroutine forever.
const terminalSendTimeout = 2 * time.Second

// startStream invokes a **stream** endpoint (POSTs body to path) and returns
// a typed channel + cancel closure that match the streaming service-interface
// signatures.
//
// The transport flow is:
//  1. POST body → 202 + StreamStartResponse{StreamID}
//  2. WS subscribe_stream(StreamID)
//  3. each "stream:<id>" frame is decoded into T and sent on the channel
//  4. terminal frame (Done=true) delivers its embedded event (the server
//     packs the final event — including any error — into the Done frame),
//     then closes the channel; cancel() also closes it
func startStream[T any](ctx context.Context, c *Client, path string, body interface{}, decode func(frame interface{}) (T, error)) (<-chan T, func(), error) {
	var resp api.StreamStartResponse
	if err := c.post(ctx, path, body, &resp); err != nil {
		return nil, func() {}, err
	}
	if resp.StreamID == "" {
		return nil, func() {}, errStreamNoID
	}
	id := resp.StreamID
	out := make(chan T, 16)

	// closeMu serializes channel sends against the close in finish() so a
	// consumer-driven cancel() can never race a send from the WS reader.
	var closeMu sync.Mutex
	closed := false
	finish := func() {
		c.unregisterStream(id)
		closeMu.Lock()
		if !closed {
			closed = true
			close(out)
		}
		closeMu.Unlock()
	}

	// Register the per-stream handler.
	c.streamMux.Lock()
	c.streamHandlers[id] = func(frame api.StreamFrame) {
		if frame.Done {
			// The terminal frame carries the final event. When the transport
			// itself failed (connection lost, unknown stream) there is no
			// event payload; synthesize one from the frame error so the
			// consumer sees the failure instead of a silent close.
			if frame.Frame == nil && frame.Error != "" {
				frame.Frame = map[string]interface{}{
					"kind":  "error",
					"error": frame.Error,
					"done":  true,
				}
			}
			if frame.Frame != nil {
				if v, err := decode(frame.Frame); err == nil {
					closeMu.Lock()
					if !closed {
						select {
						case out <- v:
						case <-time.After(terminalSendTimeout):
						}
					}
					closeMu.Unlock()
				}
			}
			finish()
			return
		}
		v, err := decode(frame.Frame)
		if err != nil {
			return
		}
		closeMu.Lock()
		if !closed {
			select {
			case out <- v:
			default:
				// Drop on backpressure; subscribers should drain.
			}
		}
		closeMu.Unlock()
	}
	c.streamMux.Unlock()

	if err := c.SendWSMessage(api.WSMsgSubscribeStream, api.SubscribeStreamPayload{StreamID: id}); err != nil {
		finish()
		return nil, func() {}, err
	}

	cancel := func() {
		_ = c.SendWSMessage(api.WSMsgCancelStream, api.CancelStreamPayload{StreamID: id})
		finish()
	}
	return out, cancel, nil
}

// unregisterStream removes the handler associated with streamID. Idempotent.
func (c *Client) unregisterStream(id string) {
	c.streamMux.Lock()
	delete(c.streamHandlers, id)
	c.streamMux.Unlock()
}

// errStreamNoID is returned when the daemon's response omits a stream_id.
var errStreamNoID = newStreamErr("server returned empty stream_id")

type streamErr struct{ msg string }

func (e *streamErr) Error() string       { return e.msg }
func newStreamErr(msg string) *streamErr { return &streamErr{msg: msg} }

// reEncode marshals frame back to JSON and unmarshals into T. Use this from
// startStream's decode callback when the underlying event has a stable JSON
// shape (every service event in internal/service/events.go satisfies this).
func reEncode[T any](frame interface{}) (T, error) {
	var zero T
	data, err := json.Marshal(frame)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, err
	}
	return out, nil
}
