package client

import (
	"context"
	"net/url"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
)

// CommitSuggestMessage calls Commit.SuggestMessage.
func (c *Client) CommitSuggestMessage(ctx context.Context, repoPath string) (string, error) {
	var resp api.CommitSuggestResponse
	if err := c.post(ctx, "/api/commit/suggest", api.RepoPathRequest{RepoPath: repoPath}, &resp); err != nil {
		return "", err
	}
	return resp.Message, nil
}

// CommitFiles calls Commit.Files.
func (c *Client) CommitFiles(ctx context.Context, repoPath, hash string) ([]api.FileChange, error) {
	var resp api.CommitFilesResponse
	q := "?repo_path=" + url.QueryEscape(repoPath) + "&hash=" + url.QueryEscape(hash)
	if err := c.get(ctx, "/api/commit/files"+q, &resp); err != nil {
		return nil, err
	}
	return resp.Files, nil
}

// CommitFileDiff calls Commit.FileDiff.
func (c *Client) CommitFileDiff(ctx context.Context, repoPath, hash, path string) (string, error) {
	var resp api.FileDiffResponse
	if err := c.post(ctx, "/api/commit/file_diff", api.CommitFileDiffRequest{RepoPath: repoPath, Hash: hash, Path: path}, &resp); err != nil {
		return "", err
	}
	return resp.Diff, nil
}

// CommitFullDiff calls Commit.FullDiff.
func (c *Client) CommitFullDiff(ctx context.Context, repoPath, hash string) (string, error) {
	var resp api.FileDiffResponse
	if err := c.post(ctx, "/api/commit/full_diff", api.CommitFullDiffRequest{RepoPath: repoPath, Hash: hash}, &resp); err != nil {
		return "", err
	}
	return resp.Diff, nil
}

// CommitCherryPick calls Commit.CherryPick.
func (c *Client) CommitCherryPick(ctx context.Context, repoPath, hash string) error {
	return c.post(ctx, "/api/commit/cherry_pick", api.CommitHashRequest{RepoPath: repoPath, Hash: hash}, nil)
}

// CommitReset calls Commit.Reset.
func (c *Client) CommitReset(ctx context.Context, repoPath, hash, mode string) error {
	return c.post(ctx, "/api/commit/reset", api.CommitResetRequest{RepoPath: repoPath, Hash: hash, Mode: mode}, nil)
}

// CommitAmend calls Commit.AmendMessage.
func (c *Client) CommitAmend(ctx context.Context, repoPath, message string) error {
	return c.post(ctx, "/api/commit/amend", api.CommitAmendRequest{RepoPath: repoPath, Message: message}, nil)
}

// CommitGenerateMessage calls Commit.GenerateMessage.
func (c *Client) CommitGenerateMessage(ctx context.Context, repoPath string) (*api.CommitMessage, error) {
	var msg api.CommitMessage
	if err := c.post(ctx, "/api/commit/message", api.CommitMessageRequest{RepoPath: repoPath}, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
