package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTrustFile(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return top
}

func projectEntry(t *testing.T, path, dir string) map[string]json.RawMessage {
	t.Helper()
	top := readTrustFile(t, path)
	projects := map[string]json.RawMessage{}
	if err := json.Unmarshal(top["projects"], &projects); err != nil {
		t.Fatalf("parse projects: %v", err)
	}
	entry := map[string]json.RawMessage{}
	if raw, ok := projects[dir]; ok {
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("parse projects[%q]: %v", dir, err)
		}
	}
	return entry
}

func TestHerdrTrustWorkDirCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", ".claude.json")
	dir := t.TempDir()
	if err := herdrTrustWorkDir(path, dir); err != nil {
		t.Fatalf("herdrTrustWorkDir: %v", err)
	}
	if got := string(projectEntry(t, path, dir)["hasTrustDialogAccepted"]); got != "true" {
		t.Errorf("hasTrustDialogAccepted = %s, want true", got)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("no lock file beside the config: %v", err)
	}
}

func TestHerdrTrustWorkDirPreservesEverythingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	dir := t.TempDir()
	// The shape claude itself writes: top-level settings beside a projects
	// map whose entries carry per-project state of their own.
	seed := `{
  "numStartups": 42,
  "oauthAccount": {"emailAddress": "someone@example.com"},
  "projects": {
    "/elsewhere": {"hasTrustDialogAccepted": true, "allowedTools": ["Bash"]},
    ` + jsonQuote(dir) + `: {"hasTrustDialogAccepted": false, "allowedTools": ["Edit"], "mcpServers": {"x": {}}}
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := herdrTrustWorkDir(path, dir); err != nil {
		t.Fatalf("herdrTrustWorkDir: %v", err)
	}

	top := readTrustFile(t, path)
	if string(top["numStartups"]) != "42" {
		t.Errorf("numStartups = %s, want 42 (top-level keys must survive)", top["numStartups"])
	}
	if !strings.Contains(string(top["oauthAccount"]), "someone@example.com") {
		t.Errorf("oauthAccount = %s, want preserved", top["oauthAccount"])
	}
	entry := projectEntry(t, path, dir)
	if string(entry["hasTrustDialogAccepted"]) != "true" {
		t.Errorf("hasTrustDialogAccepted = %s, want true (flipped from false)", entry["hasTrustDialogAccepted"])
	}
	if compact(entry["allowedTools"]) != `["Edit"]` || compact(entry["mcpServers"]) != `{"x":{}}` {
		t.Errorf("sibling fields of the entry changed: %v", entry)
	}
	other := projectEntry(t, path, "/elsewhere")
	if compact(other["allowedTools"]) != `["Bash"]` {
		t.Errorf("another project's entry changed: %v", other)
	}
}

func TestHerdrTrustWorkDirIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	dir := t.TempDir()
	if err := herdrTrustWorkDir(path, dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Already trusted: no rewrite at all, so claude's own concurrent write
	// is not raced for nothing.
	if err := herdrTrustWorkDir(path, dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("file rewritten although the dir was already trusted")
	}
}

func TestHerdrTrustWorkDirRefusesToClobberGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := herdrTrustWorkDir(path, t.TempDir()); err == nil {
		t.Fatal("herdrTrustWorkDir on an unparseable file returned nil, want an error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{not json" {
		t.Errorf("unparseable file was overwritten: %q", data)
	}
}

func TestHerdrTrustWorkDirMarksSymlinkTargetToo(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := herdrTrustWorkDir(path, link); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(link)
	for _, d := range []string{link, resolved} {
		if got := string(projectEntry(t, path, d)["hasTrustDialogAccepted"]); got != "true" {
			t.Errorf("projects[%q].hasTrustDialogAccepted = %s, want true", d, got)
		}
	}
}

// The driver marks its cwd trusted before it creates the pane: the dialog
// is what blocked the first prompt of every agent on a fresh worktree.
func TestHerdrDriverStartTrustsWorkDir(t *testing.T) {
	fakeHerdr(t, "")
	var out bytes.Buffer
	d := newTestDriver(t, &out)
	if code := d.run(strings.NewReader("")); code != 0 {
		t.Fatalf("run() = %d, want 0\n%s", code, out.String())
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(projectEntry(t, d.trustFile, cwd)["hasTrustDialogAccepted"]); got != "true" {
		t.Errorf("driver did not trust its cwd %s: hasTrustDialogAccepted = %s", cwd, got)
	}
	if strings.Contains(out.String(), "could not mark") {
		t.Errorf("trust step reported a failure:\n%s", out.String())
	}
}

// compact strips the indentation MarshalIndent gives nested raw values.
func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// jsonQuote quotes a path as a JSON string for the seed documents above.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
