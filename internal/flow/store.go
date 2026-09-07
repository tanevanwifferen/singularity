package flow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// persisted is the on-disk shape of one flow. Unlike queue's persisted, no
// projection is needed: Flow is already the whole record with snake_case
// tags, rounds and verdicts included, so an alias keeps one truth about the
// shape while giving Load the same signature queue.Store.Load has.
type persisted = Flow

// Store persists flows as JSON files under Dir, one file per flow holding the
// whole record. This is internal/queue/store.go with Flow in place of
// queueState, copied on purpose: the daemon has one persistence idiom to
// learn rather than two.
//
// A zero Store (Dir == "") is a valid no-op store: tests get a
// persistence-free manager without a temp dir, and a daemon whose state
// directory is unwritable still runs flows in memory.
type Store struct {
	Dir string
}

// NewStore returns a Store rooted at dir, creating the directory if needed.
// An empty dir yields a no-op store.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return &Store{}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create flow state dir %s: %w", dir, err)
	}
	return &Store{Dir: dir}, nil
}

// enabled reports whether this store actually touches disk.
func (s *Store) enabled() bool { return s != nil && s.Dir != "" }

// path returns the file backing one flow. Flow IDs are minted by this
// package, so this cannot escape Dir; the Base call is belt-and-braces
// against a hand-edited state file.
func (s *Store) path(flowID string) string {
	return filepath.Join(s.Dir, filepath.Base(flowID)+".json")
}

// VerdictDir returns the directory the flow's reviewers write their verdict
// files into: <Dir>/<flow_id>/r<N>-a<attempt>-verdict.json. It sits beside
// the record rather than in the work tree, so no verdict can be committed by
// an agent that was told to commit its work.
func (s *Store) VerdictDir(flowID string) string {
	if !s.enabled() {
		return ""
	}
	return filepath.Join(s.Dir, filepath.Base(flowID))
}

// Save writes one flow's state atomically: a temp file in the same directory
// followed by a rename, so a crash mid-write leaves the previous version
// intact rather than a truncated file.
func (s *Store) Save(f *Flow) error {
	if !s.enabled() || f == nil {
		return nil
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal flow %s: %w", f.ID, err)
	}
	final := s.path(f.ID)
	tmp, err := os.CreateTemp(s.Dir, ".flow-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file for flow %s: %w", f.ID, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Only removes the temp path if the rename never happened.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write flow %s: %w", f.ID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close flow %s: %w", f.ID, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod flow %s: %w", f.ID, err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("rename flow %s: %w", f.ID, err)
	}
	return nil
}

// Delete removes a flow's state file and its verdict directory. Missing
// paths are not an error. Both go together: the verdicts are only ever read
// through the record that names their round and attempt, so leaving them
// behind would only leak disk.
func (s *Store) Delete(flowID string) error {
	if !s.enabled() {
		return nil
	}
	if err := os.Remove(s.path(flowID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(s.VerdictDir(flowID)); err != nil {
		return err
	}
	return nil
}

// Load reads every persisted flow. Files that fail to parse are skipped and
// reported in the returned error list rather than aborting the load: one
// corrupt flow must not stop the daemon from serving the rest.
//
// Only *.json entries are considered, which is also what keeps a temp file
// left by a crashed Save (".flow-*.tmp") from being loaded as a flow.
func (s *Store) Load() ([]persisted, []error) {
	if !s.enabled() {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read flow state dir: %w", err)}
	}

	var out []persisted
	var problems []error
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		// Directories are the per-flow verdict dirs, not records.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	// Deterministic order so ID-sequence recovery is reproducible.
	sort.Strings(names)

	for _, name := range names {
		full := filepath.Join(s.Dir, name)
		data, rerr := os.ReadFile(full)
		if rerr != nil {
			problems = append(problems, fmt.Errorf("read %s: %w", name, rerr))
			continue
		}
		var rec persisted
		if uerr := json.Unmarshal(data, &rec); uerr != nil {
			problems = append(problems, fmt.Errorf("parse %s: %w", name, uerr))
			continue
		}
		if rec.ID == "" {
			rec.ID = strings.TrimSuffix(name, ".json")
		}
		out = append(out, rec)
	}
	return out, problems
}
