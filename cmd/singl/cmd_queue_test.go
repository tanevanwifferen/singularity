package main

import (
	"flag"
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/api"
	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

// TestParseBatchFile covers the --file contract an orchestrating agent
// writes by hand: the top-level queue default, local-name dependencies
// passed through for the daemon to resolve, and the opts block.
func TestParseBatchFile(t *testing.T) {
	doc := `{
	  "queue": "review-1",
	  "tasks": [
	    {"name":"impl","title":"implement","workdir":"/w/api","prompt":"do it",
	     "opts":{"model":"sonnet","effort":"medium","timeout_secs":1800,"use_worktree":true},
	     "priority":2,"max_retries":1,"on_failure":"continue"},
	    {"name":"review","workdir":"/w/api","prompt":"review it","after":["impl"]},
	    {"queue":"other","workdir":"/w/web","prompt":"unrelated"}
	  ]
	}`
	specs, err := parseBatchFile([]byte(doc))
	if err != nil {
		t.Fatalf("parseBatchFile: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3", len(specs))
	}

	impl := specs[0]
	if impl.Name != "impl" || impl.QueueID != "review-1" || impl.Title != "implement" {
		t.Errorf("impl spec = %+v", impl)
	}
	if impl.WorkDir != "/w/api" || impl.Prompt != "do it" {
		t.Errorf("impl work dir/prompt lost: %+v", impl)
	}
	if impl.Opts.Model != "sonnet" || impl.Opts.Effort != "medium" ||
		impl.Opts.TimeoutSecs != 1800 || !impl.Opts.UseWorktree {
		t.Errorf("impl opts = %+v", impl.Opts)
	}
	if impl.Priority != 2 || impl.MaxRetries != 1 || impl.OnFailure != service.FailContinue {
		t.Errorf("impl policy fields = %+v", impl)
	}

	// Local names travel through untouched: the daemon resolves them to
	// assigned IDs, which is the whole point of submitting a batch.
	if len(specs[1].After) != 1 || specs[1].After[0] != "impl" {
		t.Errorf("review spec lost its dependency: %+v", specs[1].After)
	}
	if specs[1].QueueID != "review-1" {
		t.Errorf("review spec did not inherit the top-level queue: %q", specs[1].QueueID)
	}
	// A per-task queue wins over the document default.
	if specs[2].QueueID != "other" {
		t.Errorf("per-task queue was overridden: %q", specs[2].QueueID)
	}
	// An unset on_failure stays empty so the manager applies its default.
	if specs[1].OnFailure != "" {
		t.Errorf("unset on_failure became %q", specs[1].OnFailure)
	}
}

// TestParseBatchFileWorkDirAlias accepts the wire spelling too, so a task
// list copied out of `queue list --json` still parses.
func TestParseBatchFileWorkDirAlias(t *testing.T) {
	specs, err := parseBatchFile([]byte(`{"tasks":[{"work_dir":"/w/api","prompt":"p"}]}`))
	if err != nil {
		t.Fatalf("parseBatchFile: %v", err)
	}
	if specs[0].WorkDir != "/w/api" {
		t.Errorf("work_dir alias ignored: %+v", specs[0])
	}
}

// TestParseBatchFileRejects covers every local validation. Anything needing
// the whole queue's state (unknown dependency, cycle) is the manager's job
// and is deliberately not listed here.
func TestParseBatchFileRejects(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"not json", `nope`, "parse task file"},
		{"no tasks", `{"tasks":[]}`, "no tasks"},
		{"missing prompt", `{"tasks":[{"workdir":"/w"}]}`, "prompt and workdir"},
		{"missing workdir", `{"tasks":[{"prompt":"p"}]}`, "prompt and workdir"},
		{"duplicate name", `{"tasks":[{"name":"a","workdir":"/w","prompt":"p"},{"name":"a","workdir":"/w","prompt":"p"}]}`, "duplicate task name"},
		{"bad on_failure", `{"tasks":[{"workdir":"/w","prompt":"p","on_failure":"explode"}]}`, "invalid on-failure"},
		{"typo'd key", `{"tasks":[{"workdir":"/w","prompts":"p"}]}`, "parse task file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseBatchFile([]byte(tc.doc))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestAfterFlagParsing pins the two spellings of --after: repeated, and
// comma-separated within one value (the shared idListFlag).
func TestAfterFlagParsing(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"--after", "t1"}, []string{"t1"}},
		{[]string{"--after", "t1,t2"}, []string{"t1", "t2"}},
		{[]string{"--after", "t1", "--after", "t2,t3"}, []string{"t1", "t2", "t3"}},
		{[]string{"--after", " t1 , t2 "}, []string{"t1", "t2"}},
		{[]string{"--after", "t1,,t2"}, []string{"t1", "t2"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			var after idListFlag
			fs.Var(&after, "after", "")
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if strings.Join(after, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v, want %v", []string(after), tc.want)
			}
		})
	}
}

// TestPathListFlagKeepsCommas guards the deliberate difference from
// idListFlag: a context file path may contain a comma.
func TestPathListFlagKeepsCommas(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var files pathListFlag
	fs.Var(&files, "context-file", "")
	if err := fs.Parse([]string{"--context-file", "/w/a,b.md", "--context-file", "/w/c.md"}); err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "/w/a,b.md" {
		t.Errorf("got %v, want the comma preserved", []string(files))
	}
}

// TestDecideQueueWait is the exit-code contract of `queue wait`.
func TestDecideQueueWait(t *testing.T) {
	cases := []struct {
		name     string
		info     api.QueueInfo
		timedOut bool
		want     queueWaitOutcome
		wantCode int
	}{
		{"still running", api.QueueInfo{Total: 2, Running: 1, Done: 1}, false, queueWaitPending, 0},
		{"still blocked", api.QueueInfo{Total: 2, Blocked: 1, Done: 1}, false, queueWaitPending, 0},
		{"drained clean", api.QueueInfo{Total: 2, Done: 2}, false, queueWaitDone, 0},
		{"drained with failure", api.QueueInfo{Total: 2, Done: 1, Failed: 1}, false, queueWaitFailed, 1},
		{"drained with cancel", api.QueueInfo{Total: 1, Cancelled: 1}, false, queueWaitFailed, 1},
		{"drained with skip", api.QueueInfo{Total: 2, Done: 1, Skipped: 1}, false, queueWaitFailed, 1},
		{"question outstanding", api.QueueInfo{Total: 2, Waiting: 1, Done: 1}, false, queueWaitAnswerNeeded, 0},
		{"question beats timeout", api.QueueInfo{Total: 1, Waiting: 1}, true, queueWaitAnswerNeeded, 0},
		{"timeout", api.QueueInfo{Total: 2, Running: 1, Done: 1}, true, queueWaitTimedOut, 1},
		{"empty queue", api.QueueInfo{}, false, queueWaitDone, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideQueueWait(tc.info, tc.timedOut)
			if got != tc.want {
				t.Fatalf("decideQueueWait = %q, want %q", got, tc.want)
			}
			if code := got.exitCode(); code != tc.wantCode {
				t.Errorf("exitCode = %d, want %d", code, tc.wantCode)
			}
		})
	}
}

// TestAggregateQueueInfo covers the bare `queue wait` (no --queue): the
// tallies of every queue add up and one paused queue marks the aggregate.
func TestAggregateQueueInfo(t *testing.T) {
	got := aggregateQueueInfo([]api.QueueInfo{
		{ID: "a", Total: 2, Running: 1, Done: 1},
		{ID: "b", Total: 3, Done: 2, Failed: 1, Paused: true},
	})
	if got.Total != 5 || got.Running != 1 || got.Done != 3 || got.Failed != 1 {
		t.Errorf("aggregate = %+v", got)
	}
	if !got.Paused {
		t.Error("a paused queue should mark the aggregate paused")
	}
	if got.ID != "" {
		t.Errorf("aggregate should not claim an ID, got %q", got.ID)
	}
	if decideQueueWait(got, false) != queueWaitPending {
		t.Error("an aggregate with a running task must still be pending")
	}
}

// TestRenderGraphTree checks the ascii rendering: dependents nest under the
// task they wait for, and a diamond's shared node is marked rather than
// duplicated endlessly.
func TestRenderGraphTree(t *testing.T) {
	out := renderGraphTree(api.QueueGraph{
		QueueID: "q1",
		Nodes: []service.GraphNode{
			{ID: "t1", Title: "impl", State: service.TaskDone},
			{ID: "t2", Title: "review", State: service.TaskRunning},
			{ID: "t3", Title: "docs", State: service.TaskReady},
			{ID: "t4", Title: "land", State: service.TaskBlocked},
		},
		Edges: []service.GraphEdge{
			{From: "t1", To: "t2"}, {From: "t1", To: "t3"},
			{From: "t2", To: "t4"}, {From: "t3", To: "t4"},
		},
	})
	for _, want := range []string{"impl [done] (t1)", "├── review", "└── docs", "land", "(again)"} {
		if !strings.Contains(out, want) {
			t.Errorf("graph output missing %q:\n%s", want, out)
		}
	}
}

// TestRenderGraphTreeOrphans keeps a cycle (or a node whose parents were
// removed) visible instead of silently dropping it.
func TestRenderGraphTreeOrphans(t *testing.T) {
	out := renderGraphTree(api.QueueGraph{
		QueueID: "q1",
		Nodes:   []service.GraphNode{{ID: "a", State: service.TaskReady}, {ID: "b", State: service.TaskReady}},
		Edges:   []service.GraphEdge{{From: "a", To: "b"}, {From: "b", To: "a"}},
	})
	if !strings.Contains(out, "unreachable from any root") {
		t.Errorf("cyclic graph lost its nodes:\n%s", out)
	}
}

// TestTypedFlagsOtherThan pins the --file mutual exclusion. The failure it
// prevents is silent: per-task flags are ignored in file mode, so
// `--file tasks.json --use-worktree --max-retries 2` used to exit 0 with
// neither applied.
func TestTypedFlagsOtherThan(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("queue-add", flag.ContinueOnError)
		fs.String("file", "", "")
		fs.String("queue", "", "")
		fs.String("workdir", "", "")
		fs.Bool("use-worktree", false, "")
		fs.Int("max-retries", 0, "")
		fs.Int("priority", 0, "")
		return fs
	}

	fs := newFS()
	if err := fs.Parse([]string{"--file", "t.json", "--queue", "q", "--use-worktree", "--max-retries", "2"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := typedFlagsOtherThan(fs, "file", "queue")
	want := []string{"max-retries", "use-worktree"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	// An explicitly passed zero value still counts as typed: that is the
	// whole reason this uses flag.Visit rather than comparing to defaults.
	fs = newFS()
	if err := fs.Parse([]string{"--file", "t.json", "--priority", "0"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := typedFlagsOtherThan(fs, "file", "queue"); len(got) != 1 || got[0] != "priority" {
		t.Fatalf("got %v, want [priority]", got)
	}

	// --file plus only --queue is the supported combination.
	fs = newFS()
	if err := fs.Parse([]string{"--file", "t.json", "--queue", "q1"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := typedFlagsOtherThan(fs, "file", "queue"); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}
