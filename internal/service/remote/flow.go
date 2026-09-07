package remote

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/client"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// remoteFlowService implements service.FlowService.
//
// As with the queue, nothing here projects between domain and wire types:
// internal/flow declared its types wire-first and internal/api aliases them,
// so every method is a straight call-through. The manager-side wiring
// (Start/Stop/Wake/Restore on flow.Manager) is absent from the interface by
// design — a remote client steers flows, it does not run the reconciler.
//
// The sentinels the local service returns — ErrNotFound for an unknown flow,
// ErrInvalidRequest for a request the manager refuses, ErrConflict for
// removing a live flow, ErrUnavailable for a daemon with no flow manager —
// arrive here intact because the SDK maps the wire code back (client.mapError),
// which is what makes a remote daemon indistinguishable from a local one.
type remoteFlowService struct {
	c *client.Client
}

// Start records a new flow and returns it pending, no rounds yet.
func (s *remoteFlowService) Start(ctx context.Context, req service.FlowStartRequest) (*service.Flow, error) {
	return s.c.FlowStart(ctx, req)
}

// List returns flows oldest first, optionally narrowed by state.
func (s *remoteFlowService) List(ctx context.Context, states []service.FlowState) ([]service.Flow, error) {
	return s.c.FlowList(ctx, states)
}

// Get returns one flow by ID, rounds and verdicts included.
func (s *remoteFlowService) Get(ctx context.Context, flowID string) (*service.Flow, error) {
	return s.c.FlowGet(ctx, flowID)
}

// Tree returns the flow's flat, parent-linked node list.
func (s *remoteFlowService) Tree(ctx context.Context, flowID string) (*service.FlowTree, error) {
	return s.c.FlowTree(ctx, flowID)
}

// Cancel marks the flow cancelled and stops the tasks it created.
func (s *remoteFlowService) Cancel(ctx context.Context, flowID string) error {
	return s.c.FlowCancel(ctx, flowID)
}

// Remove deletes a terminal flow's record and verdict directory.
func (s *remoteFlowService) Remove(ctx context.Context, flowID string) error {
	return s.c.FlowRemove(ctx, flowID)
}
