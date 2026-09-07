package local

import (
	"context"
	"errors"

	"gitlab.com/tanevanwifferen1/singularity/internal/engine"
	"gitlab.com/tanevanwifferen1/singularity/internal/flow"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localFlowService implements service.FlowService over the daemon's
// *flow.Manager.
//
// The manager may be nil, the same way localQueueService's may: a daemon
// that could not open its flow state directory still serves every other
// capability, and every method here answers ErrUnavailable rather than
// panicking. That is what lets the daemon degrade instead of refusing to
// start.
//
// The engine is held only to fill in a tree step's AgentState. The flow
// manager cannot: it reaches the queue through its narrow TaskQueue
// interface, which exposes tasks and not agents, so it knows a step's agent
// ID and nothing more. This layer already owns an engine, so the join
// happens here — exactly where flow/tree.go's comment says it should.
type localFlowService struct {
	mgr *flow.Manager
	eng *engine.Engine
}

// Start validates a request and records a new flow.
func (s *localFlowService) Start(ctx context.Context, req service.FlowStartRequest) (*service.Flow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	f, err := s.mgr.Start(req)
	if err != nil {
		return nil, mapFlowErr(err)
	}
	return &f, nil
}

// List returns flows, optionally filtered by state.
func (s *localFlowService) List(ctx context.Context, states []service.FlowState) ([]service.Flow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	flows, err := s.mgr.List(states)
	if err != nil {
		return nil, mapFlowErr(err)
	}
	return flows, nil
}

// Get returns one flow by ID.
func (s *localFlowService) Get(ctx context.Context, flowID string) (*service.Flow, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	f, err := s.mgr.Get(flowID)
	if err != nil {
		return nil, mapFlowErr(err)
	}
	return &f, nil
}

// Tree returns the flow's node list, with each step's agent state filled in
// from the engine.
func (s *localFlowService) Tree(ctx context.Context, flowID string) (*service.FlowTree, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	if s.mgr == nil {
		return nil, service.ErrUnavailable
	}
	t, err := s.mgr.Tree(flowID)
	if err != nil {
		return nil, mapFlowErr(err)
	}
	s.fillAgentStates(&t)
	return &t, nil
}

// Cancel marks the flow cancelled and stops the tasks it created.
func (s *localFlowService) Cancel(ctx context.Context, flowID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapFlowErr(s.mgr.Cancel(flowID))
}

// Remove deletes a terminal flow's record and its verdict directory.
func (s *localFlowService) Remove(ctx context.Context, flowID string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if s.mgr == nil {
		return service.ErrUnavailable
	}
	return mapFlowErr(s.mgr.Remove(flowID))
}

// fillAgentStates annotates every step node that names an agent with that
// agent's engine state. An agent the engine has since forgotten leaves the
// field empty rather than dropping the node's AgentID: the transcript may be
// gone, but the step still ran under that ID and the tree should say so.
func (s *localFlowService) fillAgentStates(t *service.FlowTree) {
	if s.eng == nil {
		return
	}
	for i := range t.Nodes {
		id := t.Nodes[i].AgentID
		if id == "" {
			continue
		}
		if a := s.eng.GetAgent(id); a != nil {
			t.Nodes[i].AgentState = a.Snapshot().State.String()
		}
	}
}

// mapFlowErr translates internal/flow's sentinels into the service ones. It
// is separate from mapErr for the same reason mapQueueErr is: flow returns
// real sentinels, and routing them through mapErr's substring matching would
// let "invalid flow request: work_dir /tmp/gone: no such file or directory"
// surface as NOT_FOUND when it is a BAD_REQUEST about the caller's input.
//
// Anything unrecognised still goes through wrapErr, so an error that
// originated deeper down (in the queue the flow submitted to, say) keeps its
// usual mapping.
func mapFlowErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, flow.ErrNotFound):
		return errFromSentinel{sentinel: service.ErrNotFound, original: err}
	case errors.Is(err, flow.ErrInvalid):
		return errFromSentinel{sentinel: service.ErrInvalidRequest, original: err}
	case errors.Is(err, flow.ErrActive):
		return errFromSentinel{sentinel: service.ErrConflict, original: err}
	}
	return wrapErr(err)
}
