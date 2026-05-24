// Package remote provides service.Services implementations backed by the
// HTTP+WebSocket client SDK in internal/client. Used by the TUI process.
//
// Every method is a thin call-through to the matching Client.<Capability>
// method. Sentinel-error round-tripping is handled inside the SDK
// (client.mapError), so the wrappers simply propagate the returned error.
//
// Streaming methods return the channel + cancel closure produced by the
// SDK's startStream helper unchanged; subscribers should call the cancel
// closure when they no longer need events. Closing the channel is the
// terminal signal.
//
// Context handling: one-shot RPCs honour ctx via the underlying
// http.Client (request cancellation propagates server-side). For
// long-lived streams, ctx is treated as best-effort — subscribers should
// invoke the returned cancel closure when ctx is done. Callers that
// require strict ctx-driven shutdown should wire a goroutine that calls
// cancel when ctx.Done() fires.
package remote

import (
	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// New builds a service.Services backed by the given client.Client. The
// caller is responsible for invoking client.Connect() before using any
// streaming method.
func New(c *client.Client) *service.Services {
	return &service.Services{
		Repo:     &remoteRepoService{c: c},
		Branch:   &remoteBranchService{c: c},
		Diff:     &remoteDiffService{c: c},
		Commit:   &remoteCommitService{c: c},
		Stash:    &remoteStashService{c: c},
		Rebase:   &remoteRebaseService{c: c},
		Worktree: &remoteWorktreeService{c: c},
		Sync:     &remoteSyncService{c: c},
		Pipeline: &remotePipelineService{c: c},
		MR:       &remoteMRService{c: c},
		Forge:    &remoteForgeService{c: c},
		Project:  &remoteProjectService{c: c},
		Agent:    &remoteAgentService{c: c},
		Jira:     &remoteJiraService{c: c},
	}
}
