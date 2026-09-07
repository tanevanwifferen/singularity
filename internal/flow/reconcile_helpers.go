package flow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gitlab.com/tanevanwifferen1/singularity/internal/queue"
)

// The reconciler's small shared helpers: the queue read, the verdict file's
// path and parse, and the naming and formatting a pass needs to describe what
// it found. Nothing here takes m.mu or makes a transition — the pass structure
// is in reconcile.go and the transitions themselves in rounds.go.

// getTask reads one task with the lock released. A nil queue reports the task
// as missing, which stops a queue-less manager from advancing a flow it could
// not have submitted in the first place.
func (m *Manager) getTask(taskID string) (queue.Task, error) {
	if m.queue == nil {
		return queue.Task{}, queue.ErrNotFound
	}
	return m.queue.Get(taskID)
}

// verdictPath is where the reviewer for one round and one attempt was told to
// write. Empty when the manager has no state directory.
func (m *Manager) verdictPath(flowID string, round, attempt int) string {
	dir := m.store.VerdictDir(flowID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, fmt.Sprintf("r%d-a%d-verdict.json", round, attempt))
}

// prepareVerdictPath returns the path and makes sure its directory exists,
// because the reviewer writes the file with a shell redirect that will not
// create one. A failure is logged rather than fatal: the flow then reads a
// missing verdict and takes §3.3, which is honest and still terminates.
func (m *Manager) prepareVerdictPath(flowID string, round, attempt int) string {
	path := m.verdictPath(flowID, round, attempt)
	if path == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("flow: create verdict dir for %s: %v", flowID, err)
	}
	return path
}

// readVerdictFile reads and parses one verdict document. A file that does not
// exist when its review task reached done is treated exactly like an
// unparseable one (§3.2), and so is a manager with nowhere to have put it: the
// alternative is inferring an accept from a task exiting cleanly, which is the
// one thing this design never does.
func readVerdictFile(path string) (*Verdict, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: no state directory is configured, so no verdict could have been written",
			ErrUnparseable)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}
	return ParseVerdict(data)
}

// stepOpts is a step's options with worktree isolation forced off. Start
// refuses use_worktree outright, so this only matters for a hand-edited state
// file — and it matters there for the reason Start refuses it.
func stepOpts(o TaskOptions) TaskOptions {
	out := o
	out.UseWorktree = false
	return out
}

// reviewTitle names a round's review step. The first attempt is plain
// `f3 r2 review`; a §3.3 re-review names its attempt, so an operator reading
// `queue list` can tell a second reviewer from a retried first one.
func reviewTitle(flowID string, round, attempt int) string {
	if attempt <= 1 {
		return fmt.Sprintf("%s r%d review", flowID, round)
	}
	return fmt.Sprintf("%s r%d review a%d", flowID, round, attempt)
}

// describeTask names a task for an error message: the ID an operator can pass
// to `queue get`, plus the title if it has one.
func describeTask(t queue.Task) string {
	if t.Title == "" {
		return "task " + t.ID
	}
	return fmt.Sprintf("task %s (%s)", t.ID, t.Title)
}

// failureReason is a task's own explanation, or a stand-in when it has none.
func failureReason(t queue.Task) string {
	if t.Error == "" {
		return "no reason recorded"
	}
	return t.Error
}

// describePath reads sensibly when there was no verdict path at all.
func describePath(path string) string {
	if path == "" {
		return "(no verdict path)"
	}
	return path
}

// findRound returns the round numbered n, or nil.
func findRound(f *Flow, n int) *Round {
	for _, r := range f.Rounds {
		if r.N == n {
			return r
		}
	}
	return nil
}

// isFlowQueueID reports whether a queue ID looks like a flow's own queue.
func isFlowQueueID(queueID string) bool {
	const prefix = "flow-"
	return len(queueID) > len(prefix) && queueID[:len(prefix)] == prefix
}
