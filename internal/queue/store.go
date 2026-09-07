package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// persisted is the on-disk shape of one queue: the queue's own metadata plus
// every task in it. One file per queue keeps writes small — a queue with a
// task transitioning does not rewrite unrelated queues — and makes the state
// directory readable by hand when something goes wrong.
type persisted struct {
	ID        string    `json:"id"`
	Paused    bool      `json:"paused"`
	CreatedAt time.Time `json:"created_at"`
	Tasks     []*Task   `json:"tasks"`
}

// Store persists queues as JSON files under Dir. A zero Store (Dir == "")
// is a valid no-op store: the daemon can run an in-memory queue when no
// state directory is available, and tests get persistence-free Managers
// without a temp dir.
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
		return nil, fmt.Errorf("create queue state dir %s: %w", dir, err)
	}
	return &Store{Dir: dir}, nil
}

// enabled reports whether this store actually touches disk.
func (s *Store) enabled() bool { return s != nil && s.Dir != "" }

// path returns the file backing one queue. Queue IDs are validated on
// creation, so this cannot escape Dir; the Base call is belt-and-braces
// against a hand-edited state file.
func (s *Store) path(queueID string) string {
	return filepath.Join(s.Dir, filepath.Base(queueID)+".json")
}

// Save writes one queue's state atomically: a temp file in the same
// directory followed by a rename, so a crash mid-write leaves the previous
// version intact rather than a truncated file.
func (s *Store) Save(q *queueState) error {
	if !s.enabled() || q == nil {
		return nil
	}
	rec := persisted{ID: q.id, Paused: q.paused, CreatedAt: q.createdAt, Tasks: q.tasks}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal queue %s: %w", q.id, err)
	}
	final := s.path(q.id)
	tmp, err := os.CreateTemp(s.Dir, ".queue-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file for queue %s: %w", q.id, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Only removes the temp path if the rename never happened.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write queue %s: %w", q.id, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close queue %s: %w", q.id, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod queue %s: %w", q.id, err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("rename queue %s: %w", q.id, err)
	}
	return nil
}

// Delete removes a queue's state file. Missing files are not an error.
func (s *Store) Delete(queueID string) error {
	if !s.enabled() {
		return nil
	}
	err := os.Remove(s.path(queueID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Load reads every persisted queue. Files that fail to parse are skipped
// and reported in the returned error list rather than aborting the load: one
// corrupt queue must not stop the daemon from serving the rest.
func (s *Store) Load() ([]persisted, []error) {
	if !s.enabled() {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("read queue state dir: %w", err)}
	}

	var out []persisted
	var problems []error
	names := make([]string, 0, len(entries))
	for _, e := range entries {
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
