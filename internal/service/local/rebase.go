package local

import (
	"context"
	"strings"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/git"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// localRebaseService implements service.RebaseService. OntoMain runs the
// existing blocking git.RebaseOntoMain in a goroutine and emits a single
// terminal SyncProgressEvent; the underlying helper doesn't stream so we
// can't either, but the channel contract is preserved.
type localRebaseService struct{}

// Plan returns the interactive-rebase commit list between base and current.
func (s *localRebaseService) Plan(ctx context.Context, repoPath, base, current string) ([]service.RebaseCommit, error) {
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	commits, err := git.GetRebasePlan(repoPath, base, current)
	if err != nil {
		return nil, wrapErr(err)
	}
	return commits, nil
}

// Status reports whether a rebase is currently in progress.
func (s *localRebaseService) Status(ctx context.Context, repoPath string) (bool, string, error) {
	if err := checkCtx(ctx); err != nil {
		return false, "", err
	}
	inProg, commit, err := git.GetRebaseStatus(repoPath)
	if err != nil {
		return inProg, commit, wrapErr(err)
	}
	return inProg, commit, nil
}

// GenerateTodo serializes a plan into git's todo-list format.
func (s *localRebaseService) GenerateTodo(ctx context.Context, commits []service.RebaseCommit) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	return git.GenerateTodoList(commits), nil
}

// Continue resumes a rebase after a conflict has been resolved.
func (s *localRebaseService) Continue(ctx context.Context, repoPath string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if err := git.ContinueRebase(repoPath); err != nil {
		// Map "no rebase in progress" specifically.
		if strings.Contains(strings.ToLower(err.Error()), "no rebase in progress") {
			return service.ErrNoRebaseInProgress
		}
		return wrapErr(err)
	}
	return nil
}

// Skip skips the current rebase step.
func (s *localRebaseService) Skip(ctx context.Context, repoPath string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if err := git.SkipRebase(repoPath); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no rebase in progress") {
			return service.ErrNoRebaseInProgress
		}
		return wrapErr(err)
	}
	return nil
}

// Abort aborts the in-progress rebase.
func (s *localRebaseService) Abort(ctx context.Context, repoPath string) error {
	if err := checkCtx(ctx); err != nil {
		return err
	}
	if err := git.AbortRebase(repoPath); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no rebase in progress") {
			return service.ErrNoRebaseInProgress
		}
		return wrapErr(err)
	}
	return nil
}

// OntoMain rebases the given worktree onto its main branch. The underlying
// git.RebaseOntoMain blocks until done; we run it in a goroutine and emit
// a single terminal event so the channel contract holds.
func (s *localRebaseService) OntoMain(ctx context.Context, repoPath string) (<-chan service.SyncProgressEvent, func(), error) {
	if err := checkCtx(ctx); err != nil {
		return nil, nil, err
	}
	out := make(chan service.SyncProgressEvent, 4)
	cctx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(out)
		mainBranch, conflicts, output, err := git.RebaseOntoMain(repoPath)
		select {
		case <-cctx.Done():
			return
		default:
		}
		ev := service.SyncProgressEvent{
			RepoPath:  repoPath,
			Op:        "rebase_onto_main",
			Line:      output,
			Done:      true,
			Timestamp: time.Now(),
		}
		if err != nil {
			ev.Err = err.Error()
		}
		if mainBranch != "" {
			ev.Line = mainBranch + ": " + output
		}
		if len(conflicts) > 0 && ev.Err == "" {
			ev.Err = "conflicts: " + strings.Join(conflicts, ", ")
		}
		select {
		case out <- ev:
		case <-cctx.Done():
		}
	}()

	return out, cancel, nil
}

// Context returns the LLM-friendly conflict-resolution context.
func (s *localRebaseService) Context(ctx context.Context, repoPath, mainBranch string, conflictFiles []string) (string, error) {
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	out, err := git.GetRebaseContext(repoPath, mainBranch, conflictFiles)
	if err != nil {
		return "", wrapErr(err)
	}
	return out, nil
}
