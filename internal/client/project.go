package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// ProjectList calls Project.List.
func (c *Client) ProjectList(ctx context.Context) ([]string, error) {
	var resp api.ProjectListResponse
	if err := c.get(ctx, "/api/project/list", &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

// ProjectLoad calls Project.Load.
func (c *Client) ProjectLoad(ctx context.Context, key string) (*api.ProjectInfo, error) {
	var info api.ProjectInfo
	if err := c.post(ctx, "/api/project/load", api.ProjectLoadRequest{Key: key}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ProjectInfo calls Project.Info.
func (c *Client) ProjectInfo(ctx context.Context, handle service.ProjectHandle) (*api.ProjectInfo, error) {
	var info api.ProjectInfo
	if err := c.get(ctx, "/api/project/info?handle="+url.QueryEscape(string(handle)), &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ProjectStatus calls Project.Status.
func (c *Client) ProjectStatus(ctx context.Context, handle service.ProjectHandle) (*api.ProjectStatus, error) {
	var st api.ProjectStatus
	if err := c.get(ctx, "/api/project/status?handle="+url.QueryEscape(string(handle)), &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ProjectRefresh calls Project.Refresh.
func (c *Client) ProjectRefresh(ctx context.Context, handle service.ProjectHandle) (*api.ProjectStatus, error) {
	var st api.ProjectStatus
	if err := c.post(ctx, "/api/project/refresh", api.ProjectHandleRequest{Handle: handle}, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ProjectBranchExists calls Project.BranchExists.
func (c *Client) ProjectBranchExists(ctx context.Context, handle service.ProjectHandle, branch string) (*api.BranchExistence, error) {
	var ex api.BranchExistence
	req := api.ProjectBranchRequest{Handle: handle, Branch: branch}
	if err := c.post(ctx, "/api/project/branch/check", req, &ex); err != nil {
		return nil, err
	}
	return &ex, nil
}

// ProjectContextSummary calls Project.ContextSummary.
func (c *Client) ProjectContextSummary(ctx context.Context, handle service.ProjectHandle) (string, error) {
	var resp api.ProjectContextResponse
	if err := c.get(ctx, "/api/project/context?handle="+url.QueryEscape(string(handle)), &resp); err != nil {
		return "", err
	}
	return resp.Context, nil
}

// ProjectDefaultConfigPath calls Project.DefaultConfigPath.
func (c *Client) ProjectDefaultConfigPath(ctx context.Context) (string, error) {
	var resp api.ProjectConfigPathResponse
	if err := c.get(ctx, "/api/project/config_path", &resp); err != nil {
		return "", err
	}
	return resp.Path, nil
}

// ProjectSubscribe calls Project.Subscribe — stream.
func (c *Client) ProjectSubscribe(ctx context.Context, handle service.ProjectHandle) (<-chan service.ProjectEvent, func(), error) {
	return startStream(ctx, c, "/api/project/subscribe", api.ProjectHandleRequest{Handle: handle}, reEncode[service.ProjectEvent])
}

// ProjectCreateWorkflow calls Project.CreateWorkflow.
func (c *Client) ProjectCreateWorkflow(ctx context.Context, handle service.ProjectHandle, branch, baseDir string) (*api.FeatureWorkflow, error) {
	var wf api.FeatureWorkflow
	req := api.WorkflowCreateRequest{Handle: handle, Branch: branch, BaseDir: baseDir}
	if err := c.post(ctx, "/api/project/workflow/create", req, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

// ProjectLoadWorkflows calls Project.LoadWorkflows.
func (c *Client) ProjectLoadWorkflows(ctx context.Context, handle service.ProjectHandle) ([]*api.FeatureWorkflow, error) {
	var resp api.WorkflowListResponse
	if err := c.get(ctx, "/api/project/workflow/list?handle="+url.QueryEscape(string(handle)), &resp); err != nil {
		return nil, err
	}
	return resp.Workflows, nil
}

// ProjectSaveWorkflows calls Project.SaveWorkflows.
func (c *Client) ProjectSaveWorkflows(ctx context.Context, handle service.ProjectHandle, workflows []*api.FeatureWorkflow) error {
	return c.post(ctx, "/api/project/workflow/save", api.WorkflowSaveRequest{Handle: handle, Workflows: workflows}, nil)
}

// ProjectDiscoverWorkflowsAllRepos calls Project.DiscoverWorkflowsAllRepos — stream.
func (c *Client) ProjectDiscoverWorkflowsAllRepos(ctx context.Context, handle service.ProjectHandle, skip map[string]bool) (<-chan service.DiscoveryProgressEvent, func(), error) {
	req := api.WorkflowDiscoverRequest{Handle: handle, Skip: skip}
	return startStream(ctx, c, "/api/project/workflow/discover", req, reEncode[service.DiscoveryProgressEvent])
}

// ProjectSubscribeWorkflows calls Project.SubscribeWorkflows — stream.
func (c *Client) ProjectSubscribeWorkflows(ctx context.Context, handle service.ProjectHandle) (<-chan service.WorkflowEvent, func(), error) {
	return startStream(ctx, c, "/api/project/workflow/subscribe", api.ProjectHandleRequest{Handle: handle}, reEncode[service.WorkflowEvent])
}
