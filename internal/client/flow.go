package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// FlowStart calls Flow.Start. The whole request travels as one body — the
// daemon cleans work_dir and the flow manager validates the rest, so a
// rejected request comes back as ErrInvalidRequest rather than being
// second-guessed here.
func (c *Client) FlowStart(ctx context.Context, req api.FlowStartRequest) (*api.Flow, error) {
	var resp api.FlowStartResponse
	if err := c.post(ctx, "/api/flow/start", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Flow, nil
}

// FlowList calls Flow.List. An empty states slice means every state. States
// are sent as repeated `state` params for the reason QueueList does it:
// the daemon accepts them comma-separated too, but url.Values spelling keeps
// values with stray spaces from being mangled.
func (c *Client) FlowList(ctx context.Context, states []api.FlowState) ([]api.Flow, error) {
	q := url.Values{}
	for _, s := range states {
		q.Add("state", string(s))
	}
	path := "/api/flow/list"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var resp api.FlowListResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Flows, nil
}

// FlowGet calls Flow.Get. The flow is the bare response body, not wrapped —
// the same shape QueueGet decodes.
func (c *Client) FlowGet(ctx context.Context, flowID string) (*api.Flow, error) {
	var f api.Flow
	if err := c.get(ctx, "/api/flow/get?flow_id="+url.QueryEscape(flowID), &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// FlowTree calls Flow.Tree: the flow's flat, parent-linked node list.
func (c *Client) FlowTree(ctx context.Context, flowID string) (*api.FlowTree, error) {
	var resp api.FlowTreeResponse
	if err := c.get(ctx, "/api/flow/tree?flow_id="+url.QueryEscape(flowID), &resp); err != nil {
		return nil, err
	}
	return &resp.Tree, nil
}

// FlowCancel calls Flow.Cancel.
func (c *Client) FlowCancel(ctx context.Context, flowID string) error {
	return c.post(ctx, "/api/flow/cancel", api.FlowIDRequest{FlowID: flowID}, nil)
}

// FlowRemove calls Flow.Remove: forgets a terminal flow and deletes its
// verdict directory. Refused with ErrConflict while the flow is running.
func (c *Client) FlowRemove(ctx context.Context, flowID string) error {
	return c.post(ctx, "/api/flow/remove", api.FlowIDRequest{FlowID: flowID}, nil)
}
