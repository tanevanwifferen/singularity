package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// TerminateWorkflowAgents ends every agent still working inside wf before its
// worktrees are torn down. Deleting a workflow removes the directories out
// from under any agent running there, so the agents have to die first or
// they are left orphaned, editing a working directory that no longer exists.
//
// A workflow can have more than one live agent: wf.AgentID only remembers the
// last one started from the workflows view, and the worktree view starts
// agents directly in worktree paths that are never recorded on the workflow
// at all. So rather than trusting the recorded ID alone, every agent the
// engine knows about whose WorkDir is wf.WorkflowDir() or below it is
// terminated, plus the recorded one. Agent state is deliberately not used
// to filter: a soft-closed agent reports killed while its process is still
// running, and Terminate on an agent whose process is already gone is a
// no-op.
//
// Terminate (not Kill) is what actually ends the subprocess, and it blocks
// until the process has been reaped, so once this returns nil the caller can
// remove the directories safely. Agents that vanished between List and
// Terminate are ignored; an unavailable agent service (no engine in this
// process) means there is nothing to kill. Any other failure is returned,
// joined per agent, and the caller should not proceed with the teardown.
func TerminateWorkflowAgents(ctx context.Context, agents AgentService, wf *FeatureWorkflow) error {
	if agents == nil || wf == nil {
		return nil
	}
	ids := make([]string, 0, 4)
	seen := make(map[string]bool)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	add(wf.GetWorkflowAgentID())

	var errs []error
	snaps, err := agents.List(ctx)
	switch {
	case errors.Is(err, ErrUnavailable):
		// No engine here, so no agents to kill.
		return nil
	case err != nil:
		errs = append(errs, fmt.Errorf("list agents: %w", err))
	default:
		dir := wf.WorkflowDir()
		for _, snap := range snaps {
			if pathWithin(snap.WorkDir, dir) {
				add(snap.ID)
			}
		}
	}

	for _, id := range ids {
		err := agents.Terminate(ctx, id)
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
			continue
		}
		errs = append(errs, fmt.Errorf("terminate agent %s: %w", id, err))
	}
	return errors.Join(errs...)
}

// TerminateAgentsWithin ends every agent whose WorkDir is dir or below it.
// It is the per-directory counterpart of TerminateWorkflowAgents, used when
// only one repo's worktree is being removed from a workflow: agents in the
// workflow's other worktrees keep running. Same semantics otherwise: an
// unavailable agent service means nothing to kill, vanished agents are
// ignored, and any other failure is returned so the caller keeps the
// directory.
func TerminateAgentsWithin(ctx context.Context, agents AgentService, dir string) error {
	if agents == nil || dir == "" {
		return nil
	}
	snaps, err := agents.List(ctx)
	if errors.Is(err, ErrUnavailable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	var errs []error
	for _, snap := range snaps {
		if !pathWithin(snap.WorkDir, dir) {
			continue
		}
		err := agents.Terminate(ctx, snap.ID)
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
			continue
		}
		errs = append(errs, fmt.Errorf("terminate agent %s: %w", snap.ID, err))
	}
	return errors.Join(errs...)
}

// pathWithin reports whether path is dir itself or lies underneath it.
func pathWithin(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
