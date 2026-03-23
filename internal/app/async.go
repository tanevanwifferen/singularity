package app

import (
	"context"
	"sync"
	"time"

	"git-frontend/internal/app/components"
	"git-frontend/internal/git"

	tea "github.com/charmbracelet/bubbletea"
)

// AsyncMsg is the base message type for async operation results.
type AsyncMsg interface {
	isAsyncMsg()
}

// AsyncOpType represents the type of async operation.
type AsyncOpType string

const (
	OpLoadRepo       AsyncOpType = "load_repo"
	OpRefreshRepo    AsyncOpType = "refresh_repo"
	OpCompareBranches AsyncOpType = "compare_branches"
	OpListBranches   AsyncOpType = "list_branches"
	OpGetStatus      AsyncOpType = "get_status"
)

// RepoLoadedMsg is sent when repository data has been loaded.
type RepoLoadedMsg struct {
	Repo *git.RepoInfo
	Err  error
}

func (RepoLoadedMsg) isAsyncMsg() {}

// BranchesLoadedMsg is sent when branch list has been loaded.
type BranchesLoadedMsg struct {
	Branches []git.BranchInfo
	Err      error
}

func (BranchesLoadedMsg) isAsyncMsg() {}

// BranchComparisonMsg is sent when branch comparison completes.
type BranchComparisonMsg struct {
	Comparison *git.BranchComparison
	BranchName string
	Err        error
}

func (BranchComparisonMsg) isAsyncMsg() {}

// StatusLoadedMsg is sent when repository status has been loaded.
type StatusLoadedMsg struct {
	IsDirty bool
	Err     error
}

func (StatusLoadedMsg) isAsyncMsg() {}

// AsyncOperation represents a tracked async operation.
type AsyncOperation struct {
	ID       string
	Type     AsyncOpType
	Cancel   context.CancelFunc
	Done     chan struct{}
	mu       sync.RWMutex
	cancelled bool
}

// AsyncManager manages async operations with cancellation and debouncing.
type AsyncManager struct {
	operations map[string]*AsyncOperation
	mu         sync.RWMutex
	debouncers map[string]*debouncer
	debMu      sync.RWMutex
	spinner    *components.Spinner
}

// debouncer handles debouncing of rapid requests.
type debouncer struct {
	timer   *time.Timer
	delay   time.Duration
	done    chan struct{}
	pending chan struct{}
}

// NewAsyncManager creates a new async manager.
func NewAsyncManager() *AsyncManager {
	return &AsyncManager{
		operations: make(map[string]*AsyncOperation),
		debouncers: make(map[string]*debouncer),
		spinner:    components.NewSpinner().SetMessage("Loading..."),
	}
}

// NewAsyncManagerWithSpinner creates a new async manager with a custom spinner message.
func NewAsyncManagerWithSpinner(message string) *AsyncManager {
	m := NewAsyncManager()
	m.spinner = components.NewSpinner().SetMessage(message)
	return m
}

// Spinner returns the manager's spinner component.
func (m *AsyncManager) Spinner() *components.Spinner {
	return m.spinner
}

// StartSpinner begins the loading spinner.
func (m *AsyncManager) StartSpinner() {
	m.spinner.Start()
}

// StopSpinner ends the loading spinner.
func (m *AsyncManager) StopSpinner() {
	m.spinner.Stop()
}

// opKey generates a unique key for an operation type with optional context.
func opKey(opType AsyncOpType, ctx ...string) string {
	if len(ctx) > 0 {
		return string(opType) + ":" + ctx[0]
	}
	return string(opType)
}

// RegisterOperation registers a new async operation.
func (m *AsyncManager) RegisterOperation(id string, opType AsyncOpType, cancel context.CancelFunc) *AsyncOperation {
	m.mu.Lock()
	defer m.mu.Unlock()

	op := &AsyncOperation{
		ID:     id,
		Type:   opType,
		Cancel: cancel,
		Done:   make(chan struct{}),
	}
	m.operations[id] = op
	return op
}

// CompleteOperation marks an operation as complete and removes it.
func (m *AsyncManager) CompleteOperation(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if op, exists := m.operations[id]; exists {
		close(op.Done)
		delete(m.operations, id)
	}
}

// CancelOperation cancels a specific operation.
func (m *AsyncManager) CancelOperation(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if op, exists := m.operations[id]; exists {
		op.mu.Lock()
		if !op.cancelled {
			op.cancelled = true
			if op.Cancel != nil {
				op.Cancel()
			}
		}
		op.mu.Unlock()
		// Remove from tracking after cancellation
		close(op.Done)
		delete(m.operations, id)
	}
}

// CancelAll cancels all in-flight operations.
func (m *AsyncManager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, op := range m.operations {
		op.mu.Lock()
		if !op.cancelled {
			op.cancelled = true
			if op.Cancel != nil {
				op.Cancel()
			}
		}
		op.mu.Unlock()
		// Remove from tracking after cancellation
		close(op.Done)
		delete(m.operations, id)
	}
}

// IsCancelled checks if an operation has been cancelled.
func (m *AsyncManager) IsCancelled(op *AsyncOperation) bool {
	if op == nil {
		return false
	}
	op.mu.RLock()
	defer op.mu.RUnlock()
	return op.cancelled
}

// ActiveOperations returns the count of active operations.
func (m *AsyncManager) ActiveOperations() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.operations)
}

// StartDebounce starts or resets a debouncer for the given key.
// The provided action will be called after the debounce delay if no new requests come in.
// Returns true if the action was immediately executed (no debounce), false if debounced.
func (m *AsyncManager) StartDebounce(key string, delay time.Duration, action func()) bool {
	m.debMu.Lock()
	defer m.debMu.Unlock()

	d, exists := m.debouncers[key]
	if !exists {
		d = &debouncer{
			delay:   delay,
			done:    make(chan struct{}),
			pending: make(chan struct{}, 1),
		}
		m.debouncers[key] = d
	}

	// If there's a pending execution, just reset the timer
	if d.timer != nil {
		d.timer.Stop()
		select {
		case <-d.pending:
		default:
		}
	}

	d.timer = time.AfterFunc(delay, func() {
		action()
		m.debMu.Lock()
		delete(m.debouncers, key)
		m.debMu.Unlock()
	})

	// Signal that there's a pending execution
	select {
	case d.pending <- struct{}{}:
	default:
	}

	return false
}

// DebouncedCall returns a channel that fires after the debounce delay.
// The returned bool indicates if this was the leading edge (true) or trailing edge (false).
func (m *AsyncManager) DebouncedCall(key string, delay time.Duration) <-chan struct{} {
	m.debMu.Lock()
	defer m.debMu.Unlock()

	d, exists := m.debouncers[key]
	if !exists {
		d = &debouncer{
			delay:   delay,
			done:    make(chan struct{}),
			pending: make(chan struct{}, 1),
		}
		m.debouncers[key] = d
	}

	// Reset timer on subsequent calls
	if d.timer != nil {
		d.timer.Stop()
		select {
		case <-d.pending:
		default:
		}
	}

	d.timer = time.AfterFunc(delay, func() {
		m.debMu.Lock()
		delete(m.debouncers, key)
		m.debMu.Unlock()
		close(d.done)
	})

	select {
	case d.pending <- struct{}{}:
	default:
	}

	return d.done
}

// CancelDebounce cancels a debounce operation.
func (m *AsyncManager) CancelDebounce(key string) {
	m.debMu.Lock()
	defer m.debMu.Unlock()

	if d, exists := m.debouncers[key]; exists {
		if d.timer != nil {
			d.timer.Stop()
		}
		delete(m.debouncers, key)
	}
}

// RunAsyncGit runs a git operation asynchronously.
// The operation function receives a context that respects cancellation.
// Returns a command that will send the result message.
func RunAsyncGit[T AsyncMsg](
	opType AsyncOpType,
	id string,
	manager *AsyncManager,
	operation func(ctx context.Context) T,
) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer manager.CompleteOperation(id)

		manager.RegisterOperation(id, opType, cancel)

		// Run the operation in a goroutine
		done := make(chan T, 1)
		go func() {
			done <- operation(ctx)
		}()

		// Wait for either completion or cancellation
		select {
		case <-ctx.Done():
			return nil // Operation was cancelled
		case result := <-done:
			return result
		}
	}
}

// RunAsyncRepoLoad loads repository info asynchronously.
func RunAsyncRepoLoad(repoPath string, manager *AsyncManager) tea.Cmd {
	id := opKey(OpLoadRepo, repoPath)
	return RunAsyncGit(OpLoadRepo, id, manager, func(ctx context.Context) RepoLoadedMsg {
		repo, err := git.OpenRepo(repoPath)
		if err != nil {
			return RepoLoadedMsg{Err: err}
		}
		return RepoLoadedMsg{Repo: repo}
	})
}

// RunAsyncBranchComparison compares two branches asynchronously.
func RunAsyncBranchComparison(repoPath, branchA, branchB string, manager *AsyncManager) tea.Cmd {
	id := opKey(OpCompareBranches, repoPath+":"+branchA+":"+branchB)
	return RunAsyncGit(OpCompareBranches, id, manager, func(ctx context.Context) BranchComparisonMsg {
		comparison, err := git.CompareBranches(repoPath, branchA, branchB)
		if err != nil {
			return BranchComparisonMsg{Err: err, BranchName: branchB}
		}
		return BranchComparisonMsg{Comparison: comparison, BranchName: branchB}
	})
}

// RunAsyncBranchList loads branch list asynchronously.
func RunAsyncBranchList(repoPath string, manager *AsyncManager) tea.Cmd {
	id := opKey(OpListBranches, repoPath)
	return RunAsyncGit(OpListBranches, id, manager, func(ctx context.Context) BranchesLoadedMsg {
		repo, err := git.OpenRepo(repoPath)
		if err != nil {
			return BranchesLoadedMsg{Err: err}
		}
		return BranchesLoadedMsg{Branches: repo.Branches}
	})
}

// RunAsyncRepoRefresh refreshes repository info asynchronously.
func RunAsyncRepoRefresh(repoPath string, manager *AsyncManager) tea.Cmd {
	id := opKey(OpRefreshRepo, repoPath)
	return RunAsyncGit(OpRefreshRepo, id, manager, func(ctx context.Context) RepoLoadedMsg {
		repo, err := git.OpenRepo(repoPath)
		if err != nil {
			return RepoLoadedMsg{Err: err}
		}
		return RepoLoadedMsg{Repo: repo}
	})
}

// AsyncManagerOption is a functional option for NewAsyncManager.
type AsyncManagerOption func(*AsyncManager)

// WithSpinnerMessage sets a custom spinner message.
func WithSpinnerMessage(msg string) AsyncManagerOption {
	return func(m *AsyncManager) {
		m.spinner = components.NewSpinner().SetMessage(msg)
	}
}
