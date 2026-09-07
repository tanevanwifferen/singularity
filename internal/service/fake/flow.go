package fake

import (
	"context"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// FlowStub is a settable service.FlowService fake.
//
// Unlike the other stubs in this package it is a struct of function fields
// rather than an empty value type, because the handler tests that consume it
// need to drive each method independently: one asserting that a NOT_FOUND
// from the service becomes a 404, the next that a valid start returns the
// flow it was given. An unset field keeps the package default — the zero
// result plus service.ErrUnavailable — so a test only writes the hooks it
// actually cares about.
//
// Not safe for concurrent mutation: set the hooks before handing the stub to
// the code under test.
type FlowStub struct {
	StartFn  func(ctx context.Context, req service.FlowStartRequest) (*service.Flow, error)
	ListFn   func(ctx context.Context, states []service.FlowState) ([]service.Flow, error)
	GetFn    func(ctx context.Context, flowID string) (*service.Flow, error)
	TreeFn   func(ctx context.Context, flowID string) (*service.FlowTree, error)
	CancelFn func(ctx context.Context, flowID string) error
	RemoveFn func(ctx context.Context, flowID string) error
}

// FlowStub must satisfy the interface the handler tests will pass it as.
var _ service.FlowService = (*FlowStub)(nil)

// NewFlowStub returns a stub with no hooks set: every method reports
// service.ErrUnavailable, matching the rest of this package.
func NewFlowStub() *FlowStub { return &FlowStub{} }

// Start records a new flow.
func (f *FlowStub) Start(ctx context.Context, req service.FlowStartRequest) (*service.Flow, error) {
	if f.StartFn == nil {
		return nil, unavail()
	}
	return f.StartFn(ctx, req)
}

// List returns flows filtered by state.
func (f *FlowStub) List(ctx context.Context, states []service.FlowState) ([]service.Flow, error) {
	if f.ListFn == nil {
		return nil, unavail()
	}
	return f.ListFn(ctx, states)
}

// Get returns one flow by ID.
func (f *FlowStub) Get(ctx context.Context, flowID string) (*service.Flow, error) {
	if f.GetFn == nil {
		return nil, unavail()
	}
	return f.GetFn(ctx, flowID)
}

// Tree returns one flow's node list.
func (f *FlowStub) Tree(ctx context.Context, flowID string) (*service.FlowTree, error) {
	if f.TreeFn == nil {
		return nil, unavail()
	}
	return f.TreeFn(ctx, flowID)
}

// Cancel stops a flow.
func (f *FlowStub) Cancel(ctx context.Context, flowID string) error {
	if f.CancelFn == nil {
		return unavail()
	}
	return f.CancelFn(ctx, flowID)
}

// Remove deletes a terminal flow's record.
func (f *FlowStub) Remove(ctx context.Context, flowID string) error {
	if f.RemoveFn == nil {
		return unavail()
	}
	return f.RemoveFn(ctx, flowID)
}
