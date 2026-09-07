package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStore builds a Store over a temp dir, failing the test if it cannot.
func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// sampleFlow is a flow with a settled round and a verdict, so a round trip
// exercises the nested records rather than just the top-level fields.
func sampleFlow(id string) *Flow {
	ended := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	return &Flow{
		ID:         id,
		QueueID:    "flow-" + id,
		Title:      "retry parsing",
		Goal:       "handle the HTTP-date form of Retry-After",
		ReviewGoal: "be adversarial about parsing",
		WorkDir:    "/tmp/work",
		MaxRounds:  3,
		Opts:       TaskOptions{ContextFiles: []string{"internal/http/retry.go"}},
		State:      StateRunning,
		Rounds: []*Round{{
			N:             1,
			WorkTaskID:    "t1",
			ReviewTaskID:  "t2",
			ReviewAttempt: 1,
			State:         RoundRejected,
			StartedAt:     ended.Add(-time.Hour),
			EndedAt:       &ended,
			Verdict: &Verdict{
				Decision: DecisionReject,
				Summary:  "date form ignored",
				Findings: []Finding{{
					Severity: SeverityBlocker,
					File:     "internal/http/retry.go",
					Line:     88,
					Detail:   "strconv.Atoi on a value that may be an HTTP-date",
				}},
			},
		}},
		CreatedAt: ended.Add(-2 * time.Hour),
	}
}

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	s := newStore(t)
	want := sampleFlow("f1")
	if err := s.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	recs, problems := s.Load()
	if len(problems) != 0 {
		t.Fatalf("Load problems: %v", problems)
	}
	if len(recs) != 1 {
		t.Fatalf("Load returned %d records, want 1", len(recs))
	}
	got := recs[0]
	if got.ID != "f1" || got.QueueID != "flow-f1" || got.Goal != want.Goal {
		t.Errorf("identity fields lost: %+v", got)
	}
	if got.State != StateRunning || got.MaxRounds != 3 {
		t.Errorf("state/cap lost: state=%q max_rounds=%d", got.State, got.MaxRounds)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if len(got.Rounds) != 1 {
		t.Fatalf("Rounds = %d, want 1", len(got.Rounds))
	}
	r := got.Rounds[0]
	if r.N != 1 || r.WorkTaskID != "t1" || r.ReviewTaskID != "t2" || r.State != RoundRejected {
		t.Errorf("round fields lost: %+v", r)
	}
	if r.EndedAt == nil || !r.EndedAt.Equal(*want.Rounds[0].EndedAt) {
		t.Errorf("round EndedAt = %v, want %v", r.EndedAt, want.Rounds[0].EndedAt)
	}
	if r.Verdict == nil {
		t.Fatal("verdict lost")
	}
	if r.Verdict.Decision != DecisionReject || len(r.Verdict.Findings) != 1 {
		t.Fatalf("verdict = %+v", r.Verdict)
	}
	if f := r.Verdict.Findings[0]; f.Severity != SeverityBlocker || f.Line != 88 {
		t.Errorf("finding lost: %+v", f)
	}
	if len(got.Opts.ContextFiles) != 1 {
		t.Errorf("Opts lost: %+v", got.Opts)
	}
}

// TestStoreSaveIsAtomic pins both halves of the rename: the record lands at
// the ID-derived path with owner-only permissions, and a temp file left by a
// crashed Save is never loaded as a flow.
func TestStoreSaveIsAtomic(t *testing.T) {
	s := newStore(t)
	if err := s.Save(sampleFlow("f1")); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(s.Dir, "f1.json"))
	if err != nil {
		t.Fatalf("stat record: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("record mode = %v, want 0600", perm)
	}

	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("Save left a temp file behind: %s", e.Name())
		}
	}

	// Simulate the crash: a half-written temp file next to the record. It
	// must be invisible to Load, and the record must still read back whole.
	tmp := filepath.Join(s.Dir, ".flow-crashed.tmp")
	if err := os.WriteFile(tmp, []byte(`{"id":"f9","state":"running"`), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	recs, problems := s.Load()
	if len(problems) != 0 {
		t.Fatalf("Load problems: %v", problems)
	}
	if len(recs) != 1 || recs[0].ID != "f1" {
		t.Fatalf("Load = %d records (%+v), want just f1", len(recs), recs)
	}
}

func TestStoreLoadSkipsCorruptFile(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"f1", "f3"} {
		if err := s.Save(sampleFlow(id)); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "f2.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	recs, problems := s.Load()
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
	if !strings.Contains(problems[0].Error(), "f2.json") {
		t.Errorf("problem does not name the bad file: %v", problems[0])
	}
	if len(recs) != 2 {
		t.Fatalf("Load = %d records, want the two good ones", len(recs))
	}
	if recs[0].ID != "f1" || recs[1].ID != "f3" {
		t.Errorf("loaded %q and %q, want f1 and f3", recs[0].ID, recs[1].ID)
	}

	// The corrupt file must not take the ID sequence down with it: f3 still
	// advances the counter.
	flows, seq := Restore(s)
	if len(flows) != 2 {
		t.Errorf("Restore returned %d flows, want 2", len(flows))
	}
	if seq != 3 {
		t.Errorf("recovered seq = %d, want 3", seq)
	}
}

// TestStoreLoadReportsUnreadableFile covers the read error arm separately
// from the parse error arm: neither is fatal.
func TestStoreLoadReportsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not deny reads")
	}
	s := newStore(t)
	if err := s.Save(sampleFlow("f1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	blind := filepath.Join(s.Dir, "f2.json")
	if err := os.WriteFile(blind, []byte("{}"), 0o000); err != nil {
		t.Fatalf("write unreadable: %v", err)
	}

	recs, problems := s.Load()
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "f2.json") {
		t.Fatalf("problems = %v, want one naming f2.json", problems)
	}
	if len(recs) != 1 || recs[0].ID != "f1" {
		t.Fatalf("Load = %+v, want just f1", recs)
	}
}

func TestZeroStoreIsANoOp(t *testing.T) {
	s, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore(\"\"): %v", err)
	}
	if s.Dir != "" {
		t.Errorf("Dir = %q, want empty", s.Dir)
	}
	if err := s.Save(sampleFlow("f1")); err != nil {
		t.Errorf("Save on zero store: %v", err)
	}
	if err := s.Delete("f1"); err != nil {
		t.Errorf("Delete on zero store: %v", err)
	}
	if dir := s.VerdictDir("f1"); dir != "" {
		t.Errorf("VerdictDir = %q, want empty", dir)
	}
	recs, problems := s.Load()
	if recs != nil || problems != nil {
		t.Errorf("Load = (%v, %v), want (nil, nil)", recs, problems)
	}
	flows, seq := Restore(s)
	if flows != nil || seq != 0 {
		t.Errorf("Restore = (%v, %d), want (nil, 0)", flows, seq)
	}

	// A nil *Store is the same no-op, so a manager built without a store
	// needs no nil checks at its call sites.
	var nilStore *Store
	if err := nilStore.Save(sampleFlow("f1")); err != nil {
		t.Errorf("Save on nil store: %v", err)
	}
	if err := nilStore.Delete("f1"); err != nil {
		t.Errorf("Delete on nil store: %v", err)
	}
}

// TestStoreLoadOnMissingDir is the unreadable-state-dir case: an absent
// directory is emptiness, not a failure.
func TestStoreLoadOnMissingDir(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "never-created")}
	recs, problems := s.Load()
	if recs != nil || problems != nil {
		t.Errorf("Load = (%v, %v), want (nil, nil)", recs, problems)
	}
}

// TestRestoreRecoversIDSequence writes a directory of records with a gap —
// the flow that used f3 was removed — and checks the counter comes back at
// the highest ID ever minted, not the number of surviving records. Minting
// from a count would hand a new flow the live ID f3.
func TestRestoreRecoversIDSequence(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"f1", "f2", "f5"} {
		if err := s.Save(sampleFlow(id)); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	flows, seq := Restore(s)
	if len(flows) != 3 {
		t.Fatalf("Restore returned %d flows, want 3", len(flows))
	}
	if seq != 5 {
		t.Errorf("recovered seq = %d, want 5", seq)
	}
	for i, want := range []string{"f1", "f2", "f5"} {
		if flows[i].ID != want {
			t.Errorf("flows[%d].ID = %q, want %q", i, flows[i].ID, want)
		}
	}
}

// TestRestoreIgnoresForeignIDs pins that only IDs this package mints move the
// counter: a hand-dropped file must not push the sequence somewhere absurd.
func TestRestoreIgnoresForeignIDs(t *testing.T) {
	s := newStore(t)
	if err := s.Save(sampleFlow("f2")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, name := range []string{"handwritten.json", "f0042x.json"} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(`{"state":"accepted"}`), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	flows, seq := Restore(s)
	if seq != 2 {
		t.Errorf("recovered seq = %d, want 2", seq)
	}
	// The foreign records still load — their ID comes from the filename —
	// so nothing is silently dropped.
	if len(flows) != 3 {
		t.Fatalf("Restore returned %d flows, want 3", len(flows))
	}
	var sawHandwritten bool
	for _, f := range flows {
		if f.ID == "handwritten" {
			sawHandwritten = true
			if f.CreatedAt.IsZero() {
				t.Error("a record with no created_at should be dated on restore")
			}
			if f.Rounds == nil {
				t.Error("Rounds should be non-nil after restore")
			}
		}
	}
	if !sawHandwritten {
		t.Error("record with no id field did not take its ID from the filename")
	}
}

// TestRestoreDoesNotReplay pins the deliberate omission: a flow recorded
// running comes back running, with its rounds untouched. The reconciler
// re-derives live state from the queue; Restore must not guess at it.
func TestRestoreDoesNotReplay(t *testing.T) {
	s := newStore(t)
	f := sampleFlow("f1")
	f.State = StateRunning
	f.Rounds[0].State = RoundRunning
	f.Rounds[0].Verdict = nil
	f.Rounds[0].EndedAt = nil
	if err := s.Save(f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	flows, _ := Restore(s)
	if len(flows) != 1 {
		t.Fatalf("Restore returned %d flows, want 1", len(flows))
	}
	got := flows[0]
	if got.State != StateRunning {
		t.Errorf("State = %q, want it left running", got.State)
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want Restore to invent none", got.Error)
	}
	if got.EndedAt != nil {
		t.Errorf("EndedAt = %v, want nil", got.EndedAt)
	}
	if got.Rounds[0].State != RoundRunning || got.Rounds[0].ReviewTaskID != "t2" {
		t.Errorf("round mutated by Restore: %+v", got.Rounds[0])
	}
}

func TestStoreDeleteRemovesRecordAndVerdictDir(t *testing.T) {
	s := newStore(t)
	if err := s.Save(sampleFlow("f1")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(sampleFlow("f2")); err != nil {
		t.Fatalf("Save f2: %v", err)
	}

	vdir := s.VerdictDir("f1")
	if want := filepath.Join(s.Dir, "f1"); vdir != want {
		t.Fatalf("VerdictDir = %q, want %q", vdir, want)
	}
	if err := os.MkdirAll(vdir, 0o700); err != nil {
		t.Fatalf("mkdir verdict dir: %v", err)
	}
	verdict := filepath.Join(vdir, "r1-a1-verdict.json")
	if err := os.WriteFile(verdict, []byte(`{"verdict":"reject"}`), 0o600); err != nil {
		t.Fatalf("write verdict: %v", err)
	}

	if err := s.Delete("f1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "f1.json")); !os.IsNotExist(err) {
		t.Errorf("record survived Delete: err = %v", err)
	}
	if _, err := os.Stat(vdir); !os.IsNotExist(err) {
		t.Errorf("verdict dir survived Delete: err = %v", err)
	}

	// Only the named flow goes.
	if _, err := os.Stat(filepath.Join(s.Dir, "f2.json")); err != nil {
		t.Errorf("Delete took an unrelated record: %v", err)
	}
	// And deleting again is not an error: Remove races with a hand-cleaned
	// state dir, and the caller has nothing useful to do about it.
	if err := s.Delete("f1"); err != nil {
		t.Errorf("second Delete: %v", err)
	}
}
