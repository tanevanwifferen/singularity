package queue

import (
	"fmt"
	"sort"
)

// depsSatisfied reports whether every dependency of t has finished in a way
// that lets t run, and whether t must instead be skipped.
//
// The three outcomes are distinct on purpose:
//
//	ready=false, skip=false → still waiting on a dependency
//	ready=false, skip=true  → a dependency failed under a "block" policy
//	ready=true              → good to dispatch
//
// A missing dependency (its task was deleted) is treated as satisfied: the
// alternative is a task wedged in blocked forever with nothing to point at.
func depsSatisfied(t *Task, all map[string]*Task) (ready bool, skip bool) {
	for _, depID := range t.DependsOn {
		dep, ok := all[depID]
		if !ok {
			continue
		}
		switch dep.State {
		case StateDone:
			continue
		case StateFailed, StateCancelled, StateSkipped:
			// The dependency's own policy decides what happens here — a
			// task does not get to opt out of being blocked by the thing
			// it depends on.
			if dep.OnFailure == FailContinue {
				continue
			}
			return false, true
		default:
			return false, false
		}
	}
	return true, false
}

// validateDeps checks a set of proposed dependency edges against the
// existing task set. It rejects unknown references, self-dependencies and
// any edge that would introduce a cycle.
//
// incoming maps a proposed task ID to the IDs it will depend on. Tasks in
// incoming may reference each other as well as existing tasks.
func validateDeps(existing map[string]*Task, incoming map[string][]string) error {
	// Build the combined adjacency: task -> its dependencies.
	deps := make(map[string][]string, len(existing)+len(incoming))
	for id, t := range existing {
		deps[id] = t.DependsOn
	}
	for id, after := range incoming {
		if _, clash := existing[id]; clash {
			return fmt.Errorf("task %s already exists", id)
		}
		deps[id] = after
	}

	for id, after := range incoming {
		for _, dep := range after {
			if dep == id {
				return fmt.Errorf("task %s cannot depend on itself", id)
			}
			if _, ok := deps[dep]; !ok {
				return fmt.Errorf("task %s depends on unknown task %s", id, dep)
			}
		}
	}

	if cycle := findCycle(deps); len(cycle) > 0 {
		return fmt.Errorf("dependency cycle: %s", formatCycle(cycle))
	}
	return nil
}

// findCycle returns the nodes of one dependency cycle, or nil when the graph
// is acyclic. Iterative three-colour DFS: grey nodes are on the current path,
// so an edge into a grey node closes a cycle.
func findCycle(deps map[string][]string) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := make(map[string]int, len(deps))

	// Deterministic iteration so the reported cycle is stable across runs —
	// map order would otherwise make the error message flap.
	roots := make([]string, 0, len(deps))
	for id := range deps {
		roots = append(roots, id)
	}
	sort.Strings(roots)

	var path []string
	var walk func(string) []string
	walk = func(id string) []string {
		switch colour[id] {
		case grey:
			// Trim the path back to where this node first appears.
			for i, p := range path {
				if p == id {
					return append(append([]string(nil), path[i:]...), id)
				}
			}
			return []string{id, id}
		case black:
			return nil
		}
		colour[id] = grey
		path = append(path, id)
		next := append([]string(nil), deps[id]...)
		sort.Strings(next)
		for _, dep := range next {
			if _, known := deps[dep]; !known {
				continue
			}
			if cyc := walk(dep); cyc != nil {
				return cyc
			}
		}
		path = path[:len(path)-1]
		colour[id] = black
		return nil
	}

	for _, id := range roots {
		if colour[id] == white {
			if cyc := walk(id); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}

// formatCycle renders a cycle as "a -> b -> a".
func formatCycle(cycle []string) string {
	out := ""
	for i, id := range cycle {
		if i > 0 {
			out += " -> "
		}
		out += id
	}
	return out
}

// dispatchOrder returns the ready tasks in the order the scheduler should
// try them: highest priority first, then oldest first. Ties break on ID so
// the order is total and reproducible.
func dispatchOrder(tasks []*Task) []*Task {
	out := append([]*Task(nil), tasks...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return out
}

// buildGraph assembles the dependency graph for one queue's tasks, with
// nodes in topological order where the DAG allows it. Tasks left over after
// the topological pass (only possible if persisted state contains a cycle)
// are appended so the graph never silently loses a node.
func buildGraph(queueID string, tasks []*Task) Graph {
	g := Graph{QueueID: queueID}
	index := make(map[string]*Task, len(tasks))
	for _, t := range tasks {
		index[t.ID] = t
	}

	// Kahn's algorithm over in-queue edges only. A dependency on a task in
	// another queue is a real edge but not a node here, so it is omitted
	// from the degree count rather than blocking the sort forever.
	indegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if _, inQueue := index[dep]; !inQueue {
				continue
			}
			indegree[t.ID]++
			dependents[dep] = append(dependents[dep], t.ID)
			g.Edges = append(g.Edges, GraphEdge{From: dep, To: t.ID})
		}
	}

	var frontier []string
	for _, t := range tasks {
		if indegree[t.ID] == 0 {
			frontier = append(frontier, t.ID)
		}
	}
	sort.Strings(frontier)

	emitted := make(map[string]bool, len(tasks))
	for len(frontier) > 0 {
		id := frontier[0]
		frontier = frontier[1:]
		t := index[id]
		if t == nil || emitted[id] {
			continue
		}
		g.Nodes = append(g.Nodes, GraphNode{ID: t.ID, Title: t.Title, State: t.State})
		emitted[id] = true

		next := append([]string(nil), dependents[id]...)
		sort.Strings(next)
		for _, dep := range next {
			indegree[dep]--
			if indegree[dep] == 0 {
				frontier = append(frontier, dep)
			}
		}
		sort.Strings(frontier)
	}

	for _, t := range tasks {
		if !emitted[t.ID] {
			g.Nodes = append(g.Nodes, GraphNode{ID: t.ID, Title: t.Title, State: t.State})
		}
	}

	sort.SliceStable(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return g
}
