package server

import (
	"encoding/json"
	"net/http"
)

// registerRoutes wires every endpoint from WIRE-CONTRACT.md.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	cors := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			h(w, r)
		}
	}
	// wrap applies CORS and, when an auth token is configured, the bearer
	// token middleware on top.
	wrap := func(h http.HandlerFunc) http.HandlerFunc {
		if s.authToken != "" {
			h = s.requireToken(h)
		}
		return cors(h)
	}

	// Status + WS + health (no service dispatch).
	mux.HandleFunc("/api/status", wrap(s.handleStatus))
	// WS upgrade goes through the auth middleware when configured but
	// deliberately bypasses CORS — gorilla's upgrader writes its own headers
	// during the hijack and `wrap` would otherwise mutate them.
	wsHandler := http.HandlerFunc(s.handleWebSocket)
	if s.authToken != "" {
		wsHandler = s.requireToken(wsHandler)
	}
	mux.Handle("/ws", wsHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Repo.
	mux.HandleFunc("/api/repo/open", wrap(s.handleRepoOpen))
	mux.HandleFunc("/api/repo/info", wrap(s.handleRepoInfo))
	mux.HandleFunc("/api/repo", wrap(s.handleRepoInfo)) // legacy alias
	mux.HandleFunc("/api/repo/find", wrap(s.handleRepoFind))
	mux.HandleFunc("/api/repo/subscribe", wrap(s.handleRepoSubscribe))

	// Branch.
	mux.HandleFunc("/api/branch/list", wrap(s.handleBranchList))
	mux.HandleFunc("/api/branch/create", wrap(s.handleBranchCreate))
	mux.HandleFunc("/api/branch/checkout", wrap(s.handleBranchCheckout))
	mux.HandleFunc("/api/branch/checkout_detached", wrap(s.handleBranchCheckoutDetached))
	mux.HandleFunc("/api/branch/checkout_detached_at", wrap(s.handleBranchCheckoutDetachedAt))
	mux.HandleFunc("/api/branch/delete", wrap(s.handleBranchDelete))
	mux.HandleFunc("/api/branch/head", wrap(s.handleBranchHEAD))
	mux.HandleFunc("/api/branch/resolve", wrap(s.handleBranchResolveRef))
	mux.HandleFunc("/api/branch/compare", wrap(s.handleBranchCompare))
	mux.HandleFunc("/api/branch/compare_tree", wrap(s.handleBranchCompareTree))
	mux.HandleFunc("/api/branch/merge", wrap(s.handleBranchMerge))
	mux.HandleFunc("/api/branch/diff", wrap(s.handleDiffBranch)) // legacy alias of /api/diff/branch

	// Diff.
	mux.HandleFunc("/api/diff/branch", wrap(s.handleDiffBranch))
	mux.HandleFunc("/api/diff/workdir", wrap(s.handleDiffWorkdir))
	mux.HandleFunc("/api/diff/file", wrap(s.handleDiffFile))
	mux.HandleFunc("/api/diff/file_staged", wrap(s.handleDiffFileStaged))
	mux.HandleFunc("/api/diff/file_unstaged", wrap(s.handleDiffFileUnstaged))
	mux.HandleFunc("/api/diff/file_deep", wrap(s.handleDiffFileDeep))
	mux.HandleFunc("/api/diff/merge_base", wrap(s.handleDiffMergeBase))
	mux.HandleFunc("/api/diff/stage_hunk", wrap(s.handleDiffStageHunk))
	mux.HandleFunc("/api/diff/unstage_hunk", wrap(s.handleDiffUnstageHunk))
	mux.HandleFunc("/api/diff/stage_lines", wrap(s.handleDiffStageLines))
	mux.HandleFunc("/api/diff/unstage_lines", wrap(s.handleDiffUnstageLines))
	mux.HandleFunc("/api/diff/all_repos", wrap(s.handleDiffAllRepos))

	// Commit.
	mux.HandleFunc("/api/commit/suggest", wrap(s.handleCommitSuggest))
	mux.HandleFunc("/api/commit/files", wrap(s.handleCommitFiles))
	mux.HandleFunc("/api/commit/file_diff", wrap(s.handleCommitFileDiff))
	mux.HandleFunc("/api/commit/full_diff", wrap(s.handleCommitFullDiff))
	mux.HandleFunc("/api/commit/cherry_pick", wrap(s.handleCommitCherryPick))
	mux.HandleFunc("/api/commit/reset", wrap(s.handleCommitReset))
	mux.HandleFunc("/api/commit/amend", wrap(s.handleCommitAmend))
	mux.HandleFunc("/api/commit/message", wrap(s.handleCommitMessage))

	// Stash.
	mux.HandleFunc("/api/stash/list", wrap(s.handleStashList))
	mux.HandleFunc("/api/stash/get", wrap(s.handleStashGet))
	mux.HandleFunc("/api/stash/create", wrap(s.handleStashCreate))
	mux.HandleFunc("/api/stash/apply", wrap(s.handleStashApply))
	mux.HandleFunc("/api/stash/drop", wrap(s.handleStashDrop))
	mux.HandleFunc("/api/stash/clear", wrap(s.handleStashClear))
	mux.HandleFunc("/api/stash/list_all", wrap(s.handleStashListAll))
	mux.HandleFunc("/api/stash/all", wrap(s.handleStashAll))
	mux.HandleFunc("/api/stash/apply_all", wrap(s.handleStashApplyAll))

	// Rebase.
	mux.HandleFunc("/api/rebase/plan", wrap(s.handleRebasePlan))
	mux.HandleFunc("/api/rebase/status", wrap(s.handleRebaseStatus))
	mux.HandleFunc("/api/rebase/todo", wrap(s.handleRebaseTodo))
	mux.HandleFunc("/api/rebase/continue", wrap(s.handleRebaseContinue))
	mux.HandleFunc("/api/rebase/skip", wrap(s.handleRebaseSkip))
	mux.HandleFunc("/api/rebase/abort", wrap(s.handleRebaseAbort))
	mux.HandleFunc("/api/rebase/onto_main", wrap(s.handleRebaseOntoMain))
	mux.HandleFunc("/api/rebase/context", wrap(s.handleRebaseContext))

	// Worktree.
	mux.HandleFunc("/api/worktree/list", wrap(s.handleWorktreeList))
	mux.HandleFunc("/api/worktree/create", wrap(s.handleWorktreeCreate))
	mux.HandleFunc("/api/worktree/remove", wrap(s.handleWorktreeRemove))
	mux.HandleFunc("/api/worktree/prune", wrap(s.handleWorktreePrune))
	mux.HandleFunc("/api/worktree/lock", wrap(s.handleWorktreeLock))
	mux.HandleFunc("/api/worktree/unlock", wrap(s.handleWorktreeUnlock))

	// Sync.
	mux.HandleFunc("/api/sync/upstream", wrap(s.handleSyncUpstream))
	mux.HandleFunc("/api/sync/last_fetch", wrap(s.handleSyncLastFetch))
	mux.HandleFunc("/api/sync/fetch", wrap(s.handleSyncFetch))
	mux.HandleFunc("/api/sync/pull", wrap(s.handleSyncPull))
	mux.HandleFunc("/api/sync/push", wrap(s.handleSyncPush))
	mux.HandleFunc("/api/sync/pull_rebase", wrap(s.handleSyncPullRebase))
	mux.HandleFunc("/api/sync/set_upstream", wrap(s.handleSyncSetUpstream))
	mux.HandleFunc("/api/sync/all", wrap(s.handleSyncAll))

	// Pipeline.
	mux.HandleFunc("/api/pipeline/statuses", wrap(s.handlePipelineStatuses))
	mux.HandleFunc("/api/pipeline/retry", wrap(s.handlePipelineRetry))
	mux.HandleFunc("/api/pipeline/subscribe", wrap(s.handlePipelineSubscribe))

	// MR.
	mux.HandleFunc("/api/mr/title", wrap(s.handleMRTitle))
	mux.HandleFunc("/api/mr/description", wrap(s.handleMRDescription))
	mux.HandleFunc("/api/mr/create", wrap(s.handleMRCreate))
	mux.HandleFunc("/api/mr/create_cli", wrap(s.handleMRCreateCLI))

	// Forge.
	mux.HandleFunc("/api/forge/auth", wrap(s.handleForgeAuth))
	mux.HandleFunc("/api/forge/info", wrap(s.handleForgeInfo))
	mux.HandleFunc("/api/forge/provider", wrap(s.handleForgeProvider))

	// Project.
	mux.HandleFunc("/api/project/list", wrap(s.handleProjectList))
	mux.HandleFunc("/api/project/load", wrap(s.handleProjectLoad))
	mux.HandleFunc("/api/project/info", wrap(s.handleProjectInfo))
	mux.HandleFunc("/api/project/status", wrap(s.handleProjectStatus))
	mux.HandleFunc("/api/project/refresh", wrap(s.handleProjectRefresh))
	mux.HandleFunc("/api/project/branch/check", wrap(s.handleProjectBranchCheck))
	mux.HandleFunc("/api/project/context", wrap(s.handleProjectContext))
	mux.HandleFunc("/api/project/config_path", wrap(s.handleProjectConfigPath))
	mux.HandleFunc("/api/project/subscribe", wrap(s.handleProjectSubscribe))
	mux.HandleFunc("/api/project/workflow/create", wrap(s.handleWorkflowCreate))
	mux.HandleFunc("/api/project/workflow/remove", wrap(s.handleWorkflowRemove))
	mux.HandleFunc("/api/project/workflow/list", wrap(s.handleWorkflowList))
	mux.HandleFunc("/api/project/workflow/save", wrap(s.handleWorkflowSave))
	mux.HandleFunc("/api/project/workflow/discover", wrap(s.handleWorkflowDiscover))
	mux.HandleFunc("/api/project/workflow/subscribe", wrap(s.handleWorkflowSubscribe))

	// Agent.
	mux.HandleFunc("/api/agent/start", wrap(s.handleAgentStart))
	mux.HandleFunc("/api/agent/resume", wrap(s.handleAgentResume))
	mux.HandleFunc("/api/agent/input", wrap(s.handleAgentInput))
	mux.HandleFunc("/api/agent/kill", wrap(s.handleAgentKill))
	mux.HandleFunc("/api/agent/remove", wrap(s.handleAgentRemove))
	mux.HandleFunc("/api/agent/list", wrap(s.handleAgentList))
	mux.HandleFunc("/api/agent/get", wrap(s.handleAgentGet))
	mux.HandleFunc("/api/agent/status", wrap(s.handleAgentGet)) // legacy alias
	mux.HandleFunc("/api/agent/output", wrap(s.handleAgentOutput))
	mux.HandleFunc("/api/agent/stats", wrap(s.handleAgentStats))
	mux.HandleFunc("/api/agent/max", wrap(s.handleAgentMax))
	mux.HandleFunc("/api/agent/subscribe", wrap(s.handleAgentSubscribe))
	mux.HandleFunc("/api/agent/subscribe_all", wrap(s.handleAgentSubscribeAll))

	// Jira.
	mux.HandleFunc("/api/jira/search", wrap(s.handleJiraSearch))
	mux.HandleFunc("/api/jira/issue", wrap(s.handleJiraIssue))
	mux.HandleFunc("/api/jira/my", wrap(s.handleJiraMy))
	mux.HandleFunc("/api/jira/update", wrap(s.handleJiraUpdate))
	mux.HandleFunc("/api/jira/comment", wrap(s.handleJiraComment))
	mux.HandleFunc("/api/jira/create", wrap(s.handleJiraCreate))
	mux.HandleFunc("/api/jira/link", wrap(s.handleJiraLink))
	mux.HandleFunc("/api/jira/actions", wrap(s.handleJiraActions))
	mux.HandleFunc("/api/jira/ai/refine", wrap(s.handleJiraRefineTicket))
	mux.HandleFunc("/api/jira/ai/stories", wrap(s.handleJiraCreateStories))
	mux.HandleFunc("/api/jira/ai/refine_proposal", wrap(s.handleJiraRefineProposal))
	mux.HandleFunc("/api/jira/ai/review", wrap(s.handleJiraReviewTickets))
}
