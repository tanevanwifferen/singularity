package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// An interactive claude launched in a directory it has not seen before
// opens its "Accessing workspace — is this a project you trust?" dialog
// before it will read a prompt. Nothing on the driver's side ever answers
// it: herdr reports the agent as blocked, `herdr agent start` returns
// agent_not_ready, and the first prompt times out with the work untouched.
// Every worktree a workflow or a flow creates is a directory claude has not
// seen before, so this is not an edge case but the common one.
//
// claude records the answer in its top-level config file, under
// projects[<cwd>].hasTrustDialogAccepted, and skips the dialog when that is
// already true. The driver writes that entry before launching the pane. The
// claude and pi backends never see the dialog (non-interactive), so this is
// herdr's alone.

// herdrClaudeConfigFile is claude's top-level config file, the one holding
// the projects map: $CLAUDE_CONFIG_DIR/.claude.json when set, else
// ~/.claude.json. Distinct from herdrClaudeConfigDir, which is the directory
// holding sessions.
func herdrClaudeConfigFile() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude.json"
	}
	return filepath.Join(home, ".claude.json")
}

// herdrTrustWorkDir marks dir as trusted in the claude config file at path,
// creating the file when it does not exist. Every other key in the file is
// preserved byte-for-byte at the JSON level: only projects[dir] is touched,
// and only its hasTrustDialogAccepted field, so an entry claude already
// wrote keeps its allowed tools, MCP servers and the rest.
//
// The symlink-resolved form of dir is marked too when it differs, because
// claude keys the map on its own process cwd and the two are not always the
// same spelling.
//
// The file is claude's, and claude rewrites it whole on its own schedule, so
// the write is an advisory-locked read-modify-write landed by rename: two
// drivers starting at once serialise on the lock, and a claude that reads
// the file mid-write sees either version but never a torn one.
func herdrTrustWorkDir(path, dir string) error {
	dirs := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		dirs = append(dirs, resolved)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck

	top := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// First launch on this machine: claude will accept a file holding
		// only the projects map and fill in the rest.
	case err != nil:
		return fmt.Errorf("read: %w", err)
	default:
		if err := json.Unmarshal(data, &top); err != nil {
			// Not ours to repair: a file claude cannot read either is a
			// louder failure than a trust dialog, and overwriting it
			// would destroy whatever else it held.
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}

	projects := map[string]json.RawMessage{}
	if raw, ok := top["projects"]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return fmt.Errorf("parse %s projects: %w", path, err)
		}
	}

	changed := false
	for _, d := range dirs {
		entry := map[string]json.RawMessage{}
		if raw, ok := projects[d]; ok && len(raw) > 0 {
			if err := json.Unmarshal(raw, &entry); err != nil {
				return fmt.Errorf("parse %s projects[%q]: %w", path, d, err)
			}
		}
		if string(entry["hasTrustDialogAccepted"]) == "true" {
			continue
		}
		entry["hasTrustDialogAccepted"] = json.RawMessage("true")
		raw, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		projects[d] = raw
		changed = true
	}
	if !changed {
		return nil
	}

	raw, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	top["projects"] = raw
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
