package client

import (
	"context"
	"encoding/json"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// startStream invokes a **stream** endpoint (POSTs body to path) and returns
// a typed channel + cancel closure that match the streaming service-interface
// signatures.
//
// The transport flow is:
//  1. POST body → 202 + StreamStartResponse{StreamID}
//  2. WS subscribe_stream(StreamID)
//  3. each "stream:<id>" frame is decoded into T and sent on the channel
//  4. terminal frame (Done=true) closes the channel; cancel() also closes it
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

	// Register the per-stream handler.
	c.streamMux.Lock()
	c.streamHandlers[id] = func(frame api.StreamFrame) {
		if frame.Done {
			c.unregisterStream(id)
			close(out)
			return
		}
		v, err := decode(frame.Frame)
		if err != nil {
			return
		}
		select {
		case out <- v:
		default:
			// Drop on backpressure; subscribers should drain.
		}
	}
	c.streamMux.Unlock()

	if err := c.SendWSMessage(api.WSMsgSubscribeStream, api.SubscribeStreamPayload{StreamID: id}); err != nil {
		c.unregisterStream(id)
		close(out)
		return nil, func() {}, err
	}

	cancel := func() {
		_ = c.SendWSMessage(api.WSMsgCancelStream, api.CancelStreamPayload{StreamID: id})
		c.unregisterStream(id)
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
