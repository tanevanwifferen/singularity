package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// projectSelection is the outcome of loading every configured project and
// picking which one the TUI opens on.
type projectSelection struct {
	Keys  []string                        // keys that loaded, in daemon order
	Key   string                          // the active one
	Infos map[string]*service.ProjectInfo // one Info per loaded key
}

// resolveProjects asks the daemon which projects are configured and loads
// them all concurrently, so the TUI can switch between them without any
// per-project load state. Projects that fail to load (missing repo path,
// bad config entry) are reported on stderr and dropped from the selection.
//
// The active project is picked in this order:
//  1. an explicit --project key,
//  2. the project owning the current working directory,
//  3. the first configured project that loaded.
//
// It returns (nil, nil) when the daemon has no project config at all — the
// caller then falls back to single-repo mode on the cwd.
func resolveProjects(svc *service.Services, wantKey string) (*projectSelection, error) {
	if svc == nil || svc.Project == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	keys, err := svc.Project.List(ctx)
	if err != nil {
		// No project config on the daemon is not an error: repo mode still works.
		if errors.Is(err, service.ErrUnavailable) {
			return nil, nil
		}
		return nil, fmt.Errorf("list projects: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	if wantKey != "" && !contains(keys, wantKey) {
		return nil, fmt.Errorf("project %q not found in config (available: %s)", wantKey, strings.Join(keys, ", "))
	}

	// Load every project concurrently. Each load walks that project's repos,
	// so the total wall time is roughly the slowest single project.
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer loadCancel()

	type loadResult struct {
		key  string
		info *service.ProjectInfo
		err  error
	}
	results := make([]loadResult, len(keys))
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			info, err := svc.Project.Load(loadCtx, key)
			results[i] = loadResult{key: key, info: info, err: err}
		}(i, key)
	}
	wg.Wait()

	sel := &projectSelection{Infos: make(map[string]*service.ProjectInfo, len(keys))}
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping project %q: %v\n", r.key, r.err)
			continue
		}
		sel.Keys = append(sel.Keys, r.key)
		sel.Infos[r.key] = r.info
	}
	if len(sel.Keys) == 0 {
		return nil, fmt.Errorf("none of the %d configured projects loaded", len(keys))
	}

	sel.Key = wantKey
	if sel.Key == "" && len(sel.Keys) > 1 {
		sel.Key = projectForCwd(sel)
	}
	if sel.Key == "" {
		sel.Key = sel.Keys[0]
	} else if !contains(sel.Keys, sel.Key) {
		return nil, fmt.Errorf("project %q failed to load (see warnings above)", sel.Key)
	}
	return sel, nil
}

// projectForCwd returns the key of the loaded project that owns the current
// working directory — i.e. one of its repo paths is the cwd or an ancestor of
// it. The deepest matching repo path wins, so a nested repo picks its own
// project. Returns "" when the cwd sits outside every configured project.
func projectForCwd(sel *projectSelection) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	cwd = filepath.Clean(cwd)

	bestKey, bestLen := "", 0
	for _, key := range sel.Keys {
		info := sel.Infos[key]
		if info == nil {
			continue
		}
		for _, repo := range info.Repos {
			p := filepath.Clean(repo.Path)
			if !pathWithin(cwd, p) {
				continue
			}
			if len(p) > bestLen {
				bestKey, bestLen = key, len(p)
			}
		}
	}
	return bestKey
}

// pathWithin reports whether path is dir itself or lives underneath it.
func pathWithin(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
