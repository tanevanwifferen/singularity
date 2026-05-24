package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// RebasePlan calls Rebase.Plan.
func (c *Client) RebasePlan(ctx context.Context, repoPath, base, current string) ([]api.RebaseCommit, error) {
	var resp api.RebasePlanResponse
	if err := c.post(ctx, "/api/rebase/plan", api.RebasePlanRequest{RepoPath: repoPath, Base: base, Current: current}, &resp); err != nil {
		return nil, err
	}
	return resp.Commits, nil
}

// RebaseStatus calls Rebase.Status.
func (c *Client) RebaseStatus(ctx context.Context, repoPath string) (bool, string, error) {
	var resp api.RebaseStatusResponse
	if err := c.get(ctx, "/api/rebase/status?repo_path="+url.QueryEscape(repoPath), &resp); err != nil {
		return false, "", err
	}
	return resp.InProgress, resp.Commit, nil
}

// RebaseGenerateTodo calls Rebase.GenerateTodo.
func (c *Client) RebaseGenerateTodo(ctx context.Context, commits []api.RebaseCommit) (string, error) {
	var resp api.RebaseTodoResponse
	if err := c.post(ctx, "/api/rebase/todo", api.RebaseTodoRequest{Commits: commits}, &resp); err != nil {
		return "", err
	}
	return resp.Todo, nil
}

// RebaseContinue calls Rebase.Continue.
func (c *Client) RebaseContinue(ctx context.Context, repoPath string) error {
	return c.post(ctx, "/api/rebase/continue", api.RepoPathRequest{RepoPath: repoPath}, nil)
}

// RebaseSkip calls Rebase.Skip.
func (c *Client) RebaseSkip(ctx context.Context, repoPath string) error {
	return c.post(ctx, "/api/rebase/skip", api.RepoPathRequest{RepoPath: repoPath}, nil)
}

// RebaseAbort calls Rebase.Abort.
func (c *Client) RebaseAbort(ctx context.Context, repoPath string) error {
	return c.post(ctx, "/api/rebase/abort", api.RepoPathRequest{RepoPath: repoPath}, nil)
}

// RebaseOntoMain calls Rebase.OntoMain — stream.
func (c *Client) RebaseOntoMain(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	return startStream(ctx, c, "/api/rebase/onto_main", api.RepoPathRequest{RepoPath: repoPath}, reEncode[service.SyncProgressEvent])
}

// RebaseContext calls Rebase.Context.
func (c *Client) RebaseContext(ctx context.Context, repoPath, mainBranch string, conflictFiles []string) (string, error) {
	var resp api.RebaseContextResponse
	req := api.RebaseContextRequest{RepoPath: repoPath, MainBranch: mainBranch, ConflictFiles: conflictFiles}
	if err := c.post(ctx, "/api/rebase/context", req, &resp); err != nil {
		return "", err
	}
	return resp.Context, nil
}
