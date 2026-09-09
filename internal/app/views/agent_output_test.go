package views

import (
	"strings"
	"testing"

	"gitlab.com/tanevanwifferen1/singularity/internal/service"
)

func foldedOutputView(t *testing.T) *AgentView {
	t.Helper()
	v := NewAgentView(t.TempDir())
	v.SetSize(80, 40)
	v.selectedAgent = &AgentInfo{ID: "agent-1"}
	v.resetOutputFolds()
	v.outputEntries = []service.OutputEntry{
		{Source: "tool_use", Content: "Read: main.go"},
		{Source: "tool_result", Content: "line one\nline two\nline three"},
		{Source: "text", Content: "done"},
	}
	v.rebuildOutputViewport()
	return v
}

// Multi-line tool blocks fold to a single header line by default; a
// one-line tool_use stays as-is and never becomes a block.
func TestMultiLineToolBlocksCollapseByDefault(t *testing.T) {
	v := foldedOutputView(t)

	if len(v.outputBlocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(v.outputBlocks))
	}
	if v.outputBlocks[0].entry != 1 {
		t.Fatalf("block entry = %d, want 1", v.outputBlocks[0].entry)
	}
	lines := strings.Split(v.outputViewport.View(), "\n")
	var header string
	for _, l := range lines {
		if strings.Contains(l, collapsedMarker) {
			header = l
		}
		if strings.Contains(l, "line two") {
			t.Fatalf("collapsed block still shows its body: %q", l)
		}
	}
	if !strings.Contains(header, "line one") || !strings.Contains(header, "(+2 lines)") {
		t.Fatalf("collapsed header = %q, want first line and hidden count", header)
	}
}

// Enter toggles the block under the cursor; with no cursor yet, the first
// visible block is used. Toggling again folds it back.
func TestToggleOutputBlockExpandsAndCollapses(t *testing.T) {
	v := foldedOutputView(t)

	v.toggleOutputBlock()
	if v.outputCursor != 0 {
		t.Fatalf("cursor = %d, want 0", v.outputCursor)
	}
	view := v.outputViewport.View()
	if !strings.Contains(view, "line three") || !strings.Contains(view, expandedMarker) {
		t.Fatalf("expanded block missing body or marker:\n%s", view)
	}

	v.toggleOutputBlock()
	if strings.Contains(v.outputViewport.View(), "line three") {
		t.Fatal("block did not fold back")
	}
}

// Expand-all applies to blocks that arrive later too, until the user
// collapses everything again.
func TestToggleAllOutputBlocksCoversNewEntries(t *testing.T) {
	v := foldedOutputView(t)

	v.toggleAllOutputBlocks()
	v.outputEntries = append(v.outputEntries, service.OutputEntry{Source: "tool_result", Content: "a\nb"})
	v.rebuildOutputViewport()
	view := v.outputViewport.View()
	if strings.Contains(view, collapsedMarker) {
		t.Fatalf("expected every block expanded:\n%s", view)
	}

	v.toggleAllOutputBlocks()
	if strings.Contains(v.outputViewport.View(), expandedMarker) {
		t.Fatal("expected every block collapsed")
	}
}
