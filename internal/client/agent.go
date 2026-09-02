package client

import (
	"context"
	"net/url"
	"strconv"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// AgentStart calls Agent.Start. The opts parameter mirrors engine.AgentOptions
// (re-exported via service.AgentOptions).
func (c *Client) AgentStart(ctx context.Context, workDir, task string, opts api.AgentOptions) (string, error) {
	req := api.AgentStartRequest{
		ProjectPath:  workDir,
		Task:         task,
		Model:        opts.Model,
		Effort:       opts.Effort,
		AllowedTools: opts.AllowedTools,
		MaxTurns:     opts.MaxTurns,
		ContextFiles: opts.ContextFiles,
		SmartRoute:   opts.SmartRoute,
		UseWorktree:  opts.UseWorktree,
		Summary:      opts.Summary,
		TimeoutSecs:  int(opts.Timeout.Seconds()),
		Backend:      opts.BackendName,
	}
	var resp api.AgentStartResponse
	if err := c.post(ctx, "/api/agent/start", req, &resp); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}

// AgentResume calls Agent.Resume.
func (c *Client) AgentResume(ctx context.Context, agentID, message string, opts api.AgentOptions) (string, error) {
	req := api.AgentResumeRequest{
		AgentID:      agentID,
		Message:      message,
		Model:        opts.Model,
		Effort:       opts.Effort,
		AllowedTools: opts.AllowedTools,
		MaxTurns:     opts.MaxTurns,
		ContextFiles: opts.ContextFiles,
		SmartRoute:   opts.SmartRoute,
		UseWorktree:  opts.UseWorktree,
		Summary:      opts.Summary,
		TimeoutSecs:  int(opts.Timeout.Seconds()),
		Backend:      opts.BackendName,
	}
	var resp api.AgentStartResponse
	if err := c.post(ctx, "/api/agent/resume", req, &resp); err != nil {
		return "", err
	}
	return resp.AgentID, nil
}

// AgentSendInput calls Agent.SendInput.
func (c *Client) AgentSendInput(ctx context.Context, agentID, message string) error {
	return c.post(ctx, "/api/agent/input", api.AgentInputRequest{AgentID: agentID, Message: message}, nil)
}

// AgentKill calls Agent.Kill.
func (c *Client) AgentKill(ctx context.Context, agentID string) error {
	return c.post(ctx, "/api/agent/kill", api.AgentQueryRequest{AgentID: agentID}, nil)
}

// AgentRemove calls Agent.Remove.
func (c *Client) AgentRemove(ctx context.Context, agentID string) error {
	return c.post(ctx, "/api/agent/remove", api.AgentQueryRequest{AgentID: agentID}, nil)
}

// AgentList calls Agent.List.
func (c *Client) AgentList(ctx context.Context) ([]api.AgentSnapshotDTO, error) {
	var resp api.AgentListResponse
	if err := c.get(ctx, "/api/agent/list", &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// AgentGet calls Agent.Get.
func (c *Client) AgentGet(ctx context.Context, agentID string) (*api.AgentSnapshotDTO, error) {
	var dto api.AgentSnapshotDTO
	if err := c.get(ctx, "/api/agent/get?agent_id="+url.QueryEscape(agentID), &dto); err != nil {
		return nil, err
	}
	return &dto, nil
}

// AgentOutput calls Agent.Output.
func (c *Client) AgentOutput(ctx context.Context, agentID string, offset int) ([]api.OutputEntry, error) {
	var resp api.AgentOutputResponse
	q := "?agent_id=" + url.QueryEscape(agentID) + "&offset=" + strconv.Itoa(offset)
	if err := c.get(ctx, "/api/agent/output"+q, &resp); err != nil {
		return nil, err
	}
	return resp.Entries, nil
}

// AgentStats calls Agent.Stats.
func (c *Client) AgentStats(ctx context.Context) (api.EngineStats, error) {
	var stats api.EngineStats
	if err := c.get(ctx, "/api/agent/stats", &stats); err != nil {
		return api.EngineStats{}, err
	}
	return stats, nil
}

// AgentMaxAgents calls Agent.MaxAgents.
func (c *Client) AgentMaxAgents(ctx context.Context) (int, error) {
	var resp api.AgentMaxResponse
	if err := c.get(ctx, "/api/agent/max", &resp); err != nil {
		return 0, err
	}
	return resp.Max, nil
}

// AgentSubscribe calls Agent.Subscribe — stream.
func (c *Client) AgentSubscribe(ctx context.Context, agentID string) (<-chan service.AgentEvent, func(), error) {
	return startStream(ctx, c, "/api/agent/subscribe", api.AgentSubscribeRequest{AgentID: agentID}, reEncode[service.AgentEvent])
}

// AgentSubscribeAll calls Agent.SubscribeAll — stream.
func (c *Client) AgentSubscribeAll(ctx context.Context) (<-chan service.AgentEvent, func(), error) {
	return startStream(ctx, c, "/api/agent/subscribe_all", struct{}{}, reEncode[service.AgentEvent])
}
