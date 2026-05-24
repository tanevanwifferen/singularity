package views

import (
	"context"
	"strings"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// viewBase provides common fields and methods shared by all views.
// Embed this struct to get SetSize, GetRepoPath, SetRepoPath, and
// services-access helpers for free.
type viewBase struct {
	width    int
	height   int
	repoPath string
	services *service.Services
}

// SetSize updates the view dimensions.
func (b *viewBase) SetSize(width, height int) {
	b.width = width
	b.height = height
}

// GetRepoPath returns the repository path.
func (b *viewBase) GetRepoPath() string {
	return b.repoPath
}

// SetRepoPath updates the repository path.
func (b *viewBase) SetRepoPath(path string) {
	b.repoPath = path
}

// SetServices wires the service container into the view. Called by the
// router/app once during view registration.
func (b *viewBase) SetServices(s *service.Services) {
	b.services = s
}

// Services returns the wired service container. May be nil for tests that
// construct views without wiring services; callers should guard.
func (b *viewBase) Services() *service.Services {
	return b.services
}

// ctx returns a fresh background context for service calls. We use a fresh
// context here rather than a view-scoped one because most view operations
// are short-lived RPCs; context cancellation will be plumbed properly in a
// later phase (TODO(phase-D-followup)).
func (b *viewBase) ctx() context.Context {
	return context.Background()
}

// maxAgents returns the engine's configured max-concurrent cap or 0 when
// the service layer is unavailable. Lets view code stay compact at call
// sites that previously had `eng.MaxAgents()` returning just an int.
func (b *viewBase) maxAgents() int {
	if b.services == nil {
		return 0
	}
	n, _ := b.services.Agent.MaxAgents(b.ctx())
	return n
}

// agentStats returns the engine-wide stats snapshot or a zero value on
// error. Mirrors the historic `eng.Stats()` int-returning shape.
func (b *viewBase) agentStats() service.EngineStats {
	if b.services == nil {
		return service.EngineStats{}
	}
	s, _ := b.services.Agent.Stats(b.ctx())
	return s
}

// agentGet returns the agent snapshot for the given ID or nil when missing
// or the service is unavailable.
func (b *viewBase) agentGet(id string) *service.AgentSnapshot {
	if b.services == nil {
		return nil
	}
	s, err := b.services.Agent.Get(b.ctx(), id)
	if err != nil {
		return nil
	}
	return s
}

// parseJiraActions reads a JiraAction list file via the JiraService. Returns
// (nil, ErrUnavailable) when the services container is missing — callers
// gracefully degrade.
func (b *viewBase) parseJiraActions(path string) ([]service.JiraAction, error) {
	return b.parseJiraActionsRaw(path)
}

// parseJiraActionsRaw is the perl-friendly stable alias used by sed scripts
// during the Phase D refactor.
func (b *viewBase) parseJiraActionsRaw(path string) ([]service.JiraAction, error) {
	if b.services == nil {
		return nil, service.ErrUnavailable
	}
	return b.services.Jira.ParseActions(b.ctx(), path)
}

// generateTodoList serializes a rebase plan back into git's todo-list format
// via the RebaseService. Returns "" when services are unavailable.
func (b *viewBase) generateTodoList(commits []service.RebaseCommit) string {
	if b.services == nil {
		return ""
	}
	s, _ := b.services.Rebase.GenerateTodo(b.ctx(), commits)
	return s
}

// drainSyncStream collects every line from a SyncProgressEvent stream into
// a single concatenated output blob, mirroring the historic blocking API
// shape (output, err). The sync views render the resulting text; their
// streaming-aware refactor is left for a follow-up phase.
//
// If the stream's terminal event carries Err, it is returned as the error
// (already mapped to a service sentinel by the implementation).
func drainSyncStream(ch <-chan service.SyncProgressEvent, cancel func(), err error) (string, error) {
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return "", err
	}
	defer cancel()
	var b strings.Builder
	var streamErr error
	for ev := range ch {
		if ev.Line != "" {
			b.WriteString(ev.Line)
			if !strings.HasSuffix(ev.Line, "\n") {
				b.WriteString("\n")
			}
		}
		if ev.Done && ev.Err != "" {
			streamErr = errFromString(ev.Err)
		}
	}
	return b.String(), streamErr
}

// fetchSync runs Fetch and drains the stream.
func (b *viewBase) fetchSync(repoPath, remote string) (string, error) {
	if b.services == nil {
		return "", service.ErrUnavailable
	}
	return drainSyncStream(b.services.Sync.Fetch(b.ctx(), repoPath, remote))
}

func (b *viewBase) pullSync(repoPath string) (string, error) {
	if b.services == nil {
		return "", service.ErrUnavailable
	}
	return drainSyncStream(b.services.Sync.Pull(b.ctx(), repoPath))
}

func (b *viewBase) pushSync(repoPath string, force bool) (string, error) {
	if b.services == nil {
		return "", service.ErrUnavailable
	}
	return drainSyncStream(b.services.Sync.Push(b.ctx(), repoPath, force))
}

func (b *viewBase) pullRebaseSync(repoPath string) (string, error) {
	if b.services == nil {
		return "", service.ErrUnavailable
	}
	return drainSyncStream(b.services.Sync.PullRebase(b.ctx(), repoPath))
}

func (b *viewBase) setUpstreamAndPushSync(repoPath, remote string) (string, error) {
	if b.services == nil {
		return "", service.ErrUnavailable
	}
	return drainSyncStream(b.services.Sync.SetUpstreamAndPush(b.ctx(), repoPath, remote))
}

// errFromString builds a basic error from a string. Used by drainSyncStream
// when the stream's terminal event carries an Err string but no sentinel.
func errFromString(s string) error {
	return errString(s)
}

type errString string

func (e errString) Error() string { return string(e) }
