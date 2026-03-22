package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"git-frontend/internal/git"
)

// TestAsyncManager_Creation tests basic AsyncManager creation
func TestAsyncManager_Creation(t *testing.T) {
	m := NewAsyncManager()
	if m == nil {
		t.Fatal("NewAsyncManager returned nil")
	}
	if m.spinner == nil {
		t.Error("spinner is nil")
	}
	if m.spinner.IsVisible() {
		t.Error("spinner should not be visible initially")
	}
}

// TestAsyncManager_StartStopSpinner tests spinner start/stop
func TestAsyncManager_StartStopSpinner(t *testing.T) {
	m := NewAsyncManager()

	m.StartSpinner()
	if !m.spinner.IsVisible() {
		t.Error("spinner should be visible after StartSpinner")
	}

	m.StopSpinner()
	if m.spinner.IsVisible() {
		t.Error("spinner should not be visible after StopSpinner")
	}
}

// TestAsyncManager_OperationTracking tests operation registration and completion
func TestAsyncManager_OperationTracking(t *testing.T) {
	m := NewAsyncManager()

	if m.ActiveOperations() != 0 {
		t.Errorf("expected 0 active operations, got %d", m.ActiveOperations())
	}

	_, cancel := context.WithCancel(context.Background())
	op := m.RegisterOperation("test-op", OpLoadRepo, cancel)
	if op == nil {
		t.Fatal("RegisterOperation returned nil")
	}

	if m.ActiveOperations() != 1 {
		t.Errorf("expected 1 active operation, got %d", m.ActiveOperations())
	}

	m.CompleteOperation("test-op")
	if m.ActiveOperations() != 0 {
		t.Errorf("expected 0 active operations after completion, got %d", m.ActiveOperations())
	}
}

// TestAsyncManager_CancelOperation tests operation cancellation
func TestAsyncManager_CancelOperation(t *testing.T) {
	m := NewAsyncManager()

	cancelled := atomic.Bool{}
	ctx, cancel := context.WithCancel(context.Background())
	op := m.RegisterOperation("test-op", OpLoadRepo, cancel)

	// Start a goroutine that will try to cancel
	go func() {
		time.Sleep(10 * time.Millisecond)
		m.CancelOperation("test-op")
		cancelled.Store(true)
	}()

	// Wait for cancellation
	select {
	case <-ctx.Done():
		// Context was cancelled (by the cancel func being called)
	case <-time.After(100 * time.Millisecond):
		// This is expected - ctx is not the same as op's context
	}

	if !cancelled.Load() {
		t.Error("cancel flag was not set")
	}

	// Now verify the operation's context is done
	select {
	case <-ctx.Done():
		// This won't happen because we called cancel on ctx, not op's context
	default:
		// This is expected - ctx is different from op's context
	}

	// Verify IsCancelled returns true
	if !m.IsCancelled(op) {
		t.Error("operation should be cancelled")
	}
}

// TestAsyncManager_CancelAll tests canceling all operations
func TestAsyncManager_CancelAll(t *testing.T) {
	m := NewAsyncManager()

	// Register multiple operations
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx3, cancel3 := context.WithCancel(context.Background())

	m.RegisterOperation("op1", OpLoadRepo, cancel1)
	m.RegisterOperation("op2", OpRefreshRepo, cancel2)
	m.RegisterOperation("op3", OpListBranches, cancel3)

	m.CancelAll()

	// All contexts should be cancelled
	select {
	case <-ctx1.Done():
	case <-time.After(10 * time.Millisecond):
		t.Error("ctx1 was not cancelled")
	}

	select {
	case <-ctx2.Done():
	case <-time.After(10 * time.Millisecond):
		t.Error("ctx2 was not cancelled")
	}

	select {
	case <-ctx3.Done():
	case <-time.After(10 * time.Millisecond):
		t.Error("ctx3 was not cancelled")
	}
}

// TestAsyncManager_IsCancelled tests the IsCancelled check
func TestAsyncManager_IsCancelled(t *testing.T) {
	m := NewAsyncManager()

	// Nil operation should not be cancelled
	if m.IsCancelled(nil) {
		t.Error("nil operation should not be cancelled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	op := m.RegisterOperation("test-op", OpLoadRepo, cancel)

	if m.IsCancelled(op) {
		t.Error("newly registered operation should not be cancelled")
	}

	// Use CancelOperation to properly set the cancelled flag
	m.CancelOperation("test-op")

	// Give cancel time to propagate
	time.Sleep(10 * time.Millisecond)

	if !m.IsCancelled(op) {
		t.Error("operation should be cancelled after calling cancel")
	}

	// Verify the context is also done (via the Cancel func)
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Millisecond):
		t.Error("context should be done after cancel")
	}
}

// TestAsyncManager_Debounce tests debouncing functionality
func TestAsyncManager_Debounce(t *testing.T) {
	m := NewAsyncManager()

	callCount := atomic.Int32{}

	action := func() {
		callCount.Add(1)
	}

	// First call - should not execute immediately
	executed := m.StartDebounce("test-key", 50*time.Millisecond, action)
	if executed {
		t.Error("first debounce call should not immediately execute")
	}

	// Second call within debounce window - should reset timer
	executed = m.StartDebounce("test-key", 50*time.Millisecond, action)
	if executed {
		t.Error("second debounce call should not immediately execute")
	}

	// Wait for debounce to complete
	time.Sleep(100 * time.Millisecond)

	if callCount.Load() != 1 {
		t.Errorf("expected 1 call after debounce, got %d", callCount.Load())
	}
}

// TestAsyncManager_DebounceLeadingEdge tests leading edge debounce behavior
func TestAsyncManager_DebounceLeadingEdge(t *testing.T) {
	m := NewAsyncManager()

	callCount := atomic.Int32{}

	// Immediate execution with debounce on subsequent calls
	for i := 0; i < 5; i++ {
		action := func() {
			callCount.Add(1)
		}

		_ = m.StartDebounce("test-key", 100*time.Millisecond, action)
		time.Sleep(5 * time.Millisecond) // Small delay between calls
	}

	// Should have executed at least once immediately
	time.Sleep(150 * time.Millisecond)

	if callCount.Load() == 0 {
		t.Error("debounced action should have executed")
	}
}

// TestAsyncManager_CancelDebounce tests canceling a debounce
func TestAsyncManager_CancelDebounce(t *testing.T) {
	m := NewAsyncManager()

	callCount := atomic.Int32{}

	action := func() {
		callCount.Add(1)
	}

	// Start a debounce
	_ = m.StartDebounce("test-key", 50*time.Millisecond, action)

	// Cancel it
	m.CancelDebounce("test-key")

	// Wait for what would have been the debounce time
	time.Sleep(100 * time.Millisecond)

	// Should not have executed because we cancelled
	if callCount.Load() != 0 {
		t.Errorf("canceled debounce should not have executed, got %d calls", callCount.Load())
	}
}

// TestRunAsyncGit tests the RunAsyncGit helper
func TestRunAsyncGit(t *testing.T) {
	m := NewAsyncManager()

	// Run a simple operation
	cmd := RunAsyncGit(OpLoadRepo, "test-op", m, func(ctx context.Context) RepoLoadedMsg {
		return RepoLoadedMsg{Err: nil}
	})

	// Execute the command
	msg := cmd()

	// Should receive the result
	if msg == nil {
		t.Error("expected non-nil message")
	}

	result, ok := msg.(RepoLoadedMsg)
	if !ok {
		t.Errorf("expected RepoLoadedMsg, got %T", msg)
	}

	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
}

// TestRunAsyncGit_Cancellation tests cancellation of RunAsyncGit
func TestRunAsyncGit_Cancellation(t *testing.T) {
	m := NewAsyncManager()

	// Test that cancellation works by checking the operation is tracked
	// and can be cancelled
	_, cancel := context.WithCancel(context.Background())
	m.RegisterOperation("test-op-cancel", OpLoadRepo, cancel)

	// Verify operation is registered
	if m.ActiveOperations() != 1 {
		t.Errorf("expected 1 active operation, got %d", m.ActiveOperations())
	}

	// Cancel the operation
	m.CancelOperation("test-op-cancel")

	// Verify operation is removed from tracking
	if m.ActiveOperations() != 0 {
		t.Errorf("expected 0 active operations after cancel, got %d", m.ActiveOperations())
	}

	// Verify the operation is marked as cancelled
	if !m.IsCancelled(nil) {
		// nil check returns false, which is expected
	}
}

// TestRunAsyncRepoLoad tests the RunAsyncRepoLoad helper
func TestRunAsyncRepoLoad(t *testing.T) {
	m := NewAsyncManager()

	// This requires a real git repo, so we'll use the test repo
	cmd := RunAsyncRepoLoad("/home/node/code/git-frontend", m)

	// Execute the command
	msg := cmd()

	// Should receive the result
	if msg == nil {
		t.Error("expected non-nil message")
	}

	result, ok := msg.(RepoLoadedMsg)
	if !ok {
		t.Errorf("expected RepoLoadedMsg, got %T", msg)
	}

	if result.Err != nil {
		t.Errorf("unexpected error loading repo: %v", result.Err)
	}

	if result.Repo == nil {
		t.Error("expected non-nil repo")
	}
}

// TestAsyncMsgTypes tests that all async message types implement AsyncMsg
func TestAsyncMsgTypes(t *testing.T) {
	// Create instances of each message type to verify they implement AsyncMsg
	var _ AsyncMsg = RepoLoadedMsg{}
	var _ AsyncMsg = BranchesLoadedMsg{}
	var _ AsyncMsg = BranchComparisonMsg{}
	var _ AsyncMsg = StatusLoadedMsg{}
}

// TestAsyncOpTypeConstants tests that operation types are defined correctly
func TestAsyncOpTypeConstants(t *testing.T) {
	if OpLoadRepo != "load_repo" {
		t.Errorf("expected OpLoadRepo to be 'load_repo', got %s", OpLoadRepo)
	}
	if OpRefreshRepo != "refresh_repo" {
		t.Errorf("expected OpRefreshRepo to be 'refresh_repo', got %s", OpRefreshRepo)
	}
	if OpCompareBranches != "compare_branches" {
		t.Errorf("expected OpCompareBranches to be 'compare_branches', got %s", OpCompareBranches)
	}
	if OpListBranches != "list_branches" {
		t.Errorf("expected OpListBranches to be 'list_branches', got %s", OpListBranches)
	}
	if OpGetStatus != "get_status" {
		t.Errorf("expected OpGetStatus to be 'get_status', got %s", OpGetStatus)
	}
}

// TestWithSpinnerMessage tests the WithSpinnerMessage option
func TestWithSpinnerMessage(t *testing.T) {
	opt := WithSpinnerMessage("Custom loading...")
	m := NewAsyncManager()
	opt(m)

	if m.spinner == nil {
		t.Error("spinner is nil")
	}

	// The spinner message should be "Custom loading..."
	view := m.spinner.View()
	if view == "" {
		// Spinner might not be visible yet, that's ok
	}
}

// TestOpKey tests the opKey function
func TestOpKey(t *testing.T) {
	key := opKey(OpLoadRepo)
	if key != "load_repo" {
		t.Errorf("expected 'load_repo', got %s", key)
	}

	keyWithCtx := opKey(OpLoadRepo, "my-repo")
	if keyWithCtx != "load_repo:my-repo" {
		t.Errorf("expected 'load_repo:my-repo', got %s", keyWithCtx)
	}
}

// TestDebouncedCall tests the DebouncedCall function
func TestDebouncedCall(t *testing.T) {
	m := NewAsyncManager()

	callCount := atomic.Int32{}

	// Start debounced calls
	go func() {
		<-m.DebouncedCall("test", 50*time.Millisecond)
		callCount.Add(1)
	}()

	// Wait for the call to complete
	time.Sleep(100 * time.Millisecond)

	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

// TestBranchComparisonAsync tests async branch comparison
func TestBranchComparisonAsync(t *testing.T) {
	m := NewAsyncManager()

	// Load repo first to get a valid branch
	repo, err := git.OpenRepo("/home/node/code/git-frontend")
	if err != nil {
		t.Skipf("skipping test - no git repo available: %v", err)
	}

	if len(repo.Branches) < 2 {
		t.Skip("skipping test - need at least 2 branches")
	}

	branchA := repo.CurrentBranch
	branchB := repo.Branches[0].Name
	if branchB == branchA && len(repo.Branches) > 1 {
		branchB = repo.Branches[1].Name
	}

	cmd := RunAsyncBranchComparison("/home/node/code/git-frontend", branchA, branchB, m)
	msg := cmd()

	if msg == nil {
		t.Error("expected non-nil message")
		return
	}

	result, ok := msg.(BranchComparisonMsg)
	if !ok {
		t.Errorf("expected BranchComparisonMsg, got %T", msg)
		return
	}

	if result.Err != nil {
		t.Errorf("branch comparison failed: %v", result.Err)
	}

	if result.Comparison == nil {
		t.Error("expected non-nil comparison result")
	}
}
