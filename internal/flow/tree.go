package flow

import (
	"fmt"
	"strings"
)

// The flow tree: one flat, parent-linked node list per flow, matching
// queue.Graph's nodes+edges shape rather than nesting rounds inside a flow.
// Flat because every client renders it without recursion, and because the
// TUI's tree pane and the CLI's --json want the same list.
//
// The tree is derivable client-side from Flow + `queue list`, but that is
// two round trips and a join every client would get subtly wrong — most
// obviously by caching a step's state, which is the one thing a Round
// deliberately does not store (§1.1). Here the join happens once, server
// side, reading each step's state from its task on demand.

// NodeKind distinguishes the three levels of the tree. A client switches on
// it rather than on an ID's shape.
type NodeKind string

const (
	// NodeFlow is the single root node.
	NodeFlow NodeKind = "flow"
	// NodeRound is one implement/fix → review cycle.
	NodeRound NodeKind = "round"
	// NodeStep is one task: a round's work step or its review step.
	NodeStep NodeKind = "step"
)

// StepStateUnknown is a step's state when the queue no longer has its task —
// `queue remove` on a finished flow's queue, say. The step existed, so
// dropping the node would misreport the round's shape; its state is simply
// not recoverable any more.
const StepStateUnknown = "unknown"

// TreeNode is one node of a flow's tree.
//
// State is a plain string because the three kinds report from three
// different enums — a flow's State, a Round's RoundState, and for a step the
// queue.State of its task. Collapsing them to one typed field would mean
// inventing a fourth enum that is the union of the others, and a renderer
// only ever displays it.
type TreeNode struct {
	// ID is hierarchical and stable: "f3", "f3/r2", "f3/r2/review".
	ID string `json:"id"`
	// ParentID is empty on the root only.
	ParentID string   `json:"parent_id,omitempty"`
	Kind     NodeKind `json:"kind"`
	Label    string   `json:"label"`
	State    string   `json:"state"`
	// Round is the 1-based round number on round and step nodes, 0 on the
	// root.
	Round  int    `json:"round,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	// AgentID is the most recent agent dispatched for a step's task; it
	// stays set after the task finishes, so the transcript remains
	// reachable from the tree.
	AgentID string `json:"agent_id,omitempty"`
	// AgentState is the engine's state name for that agent. The flow
	// manager cannot fill it: it reaches the queue through TaskQueue, which
	// exposes tasks and not agents, and queue.Manager keeps agent state
	// behind its own AgentRunner. Whoever wires the tree to an engine
	// (internal/service/local, which already holds one) fills it in.
	AgentState string `json:"agent_state,omitempty"`
	// Verdict is set on a round node whose review produced one — including
	// the synthetic reject that stands in for an unparseable review. Steps
	// carry none: the verdict is the round's outcome, and duplicating it
	// onto the review step would be two truths about one decision.
	Verdict *Verdict `json:"verdict,omitempty"`
}

// Tree is a flow's node list.
type Tree struct {
	FlowID string     `json:"flow_id"`
	Nodes  []TreeNode `json:"nodes"`
}

// Tree returns the flow's tree: the root, one node per round, and one node
// per step with its task's live state.
//
// Step state is read from the queue here and never from the flow record, so
// a task an operator cancelled or retried behind the flow's back shows up as
// what it is. The queue reads happen with the manager's lock released, for
// the reason every other engine-facing call in the daemon does: the
// reconciler registers itself as a queue change observer, and a queue call
// under m.mu would put the two locks in opposite orders.
func (m *Manager) Tree(flowID string) (Tree, error) {
	m.mu.Lock()
	f, ok := m.flows[flowID]
	if !ok {
		m.mu.Unlock()
		return Tree{}, ErrNotFound
	}
	snapshot := f.Clone()
	m.mu.Unlock()

	return m.buildTree(&snapshot), nil
}

// buildTree assembles the node list from a flow snapshot, asking the queue
// for each step's task. It takes no lock: snapshot is the caller's private
// copy.
func (m *Manager) buildTree(f *Flow) Tree {
	out := Tree{FlowID: f.ID}
	out.Nodes = append(out.Nodes, TreeNode{
		ID:    f.ID,
		Kind:  NodeFlow,
		Label: flowLabel(f),
		State: string(f.State),
	})

	if f.PlanTaskID != "" {
		out.Nodes = append(out.Nodes, m.stepNode(f.ID, "plan", 0, f.PlanTaskID))
	}

	for _, r := range f.Rounds {
		roundID := fmt.Sprintf("%s/r%d", f.ID, r.N)
		out.Nodes = append(out.Nodes, TreeNode{
			ID:       roundID,
			ParentID: f.ID,
			Kind:     NodeRound,
			Label:    fmt.Sprintf("round %d", r.N),
			State:    string(r.State),
			Round:    r.N,
			Verdict:  r.Verdict,
		})
		// Work step then review step, which is both submit order and the
		// order they run in.
		if r.WorkTaskID != "" {
			out.Nodes = append(out.Nodes, m.stepNode(roundID, workStepLabel(r.N), r.N, r.WorkTaskID))
		}
		if r.ReviewTaskID != "" {
			out.Nodes = append(out.Nodes, m.stepNode(roundID, "review", r.N, r.ReviewTaskID))
		}
	}
	return out
}

// stepNode builds one step node, reading its state and agent from the task.
// A task the queue cannot produce still yields a node — see
// StepStateUnknown.
func (m *Manager) stepNode(parentID, label string, round int, taskID string) TreeNode {
	node := TreeNode{
		ID:       parentID + "/" + label,
		ParentID: parentID,
		Kind:     NodeStep,
		Label:    label,
		State:    StepStateUnknown,
		Round:    round,
		TaskID:   taskID,
	}
	if m.queue == nil {
		return node
	}
	t, err := m.queue.Get(taskID)
	if err != nil {
		return node
	}
	node.State = string(t.State)
	node.AgentID = t.AgentID
	return node
}

// workStepLabel names a round's work step for what it does: round 1
// implements, every later round fixes the findings of the one before. The
// label is part of the node ID, so it is also how a client addresses the
// step.
func workStepLabel(round int) string {
	if round <= 1 {
		return "implement"
	}
	return "fix"
}

// flowLabel is the root node's label: the title if the caller gave one, and
// otherwise the first line of the goal, shortened. A root labelled with an
// empty string would leave the TUI's tree pane headed by nothing at all.
func flowLabel(f *Flow) string {
	if t := strings.TrimSpace(f.Title); t != "" {
		return t
	}
	line := strings.TrimSpace(f.Goal)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	const max = 60
	if len(line) > max {
		return strings.TrimSpace(line[:max]) + "…"
	}
	return line
}
