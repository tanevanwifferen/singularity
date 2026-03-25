package views

// diffNavHelper manages shared diff-panel state for multi-repo diff views
// (WorkflowDiffView and ProjectDiffView).
type diffNavHelper struct {
	selectedIdx      int
	showDiff         bool
	currentDiff      string
	parsedDiffLines  []DiffLine
	diffScrollOffset int
}

// closeDiff resets diff panel state to its zero value.
func (h *diffNavHelper) closeDiff() {
	h.showDiff = false
	h.currentDiff = ""
	h.parsedDiffLines = nil
	h.diffScrollOffset = 0
}

// clampIndex constrains idx to [0, itemCount) and returns the result.
// Returns -1 when itemCount is 0.
func clampIndex(idx, itemCount int) int {
	if itemCount == 0 {
		return -1
	}
	if idx < 0 {
		return 0
	}
	if idx >= itemCount {
		return itemCount - 1
	}
	return idx
}
