package views

// RefreshMsg is a request to refresh the view data
type RefreshMsg struct{}

// OpenPRForBranchMsg requests the PR/MR creation view to open with a specific source branch pre-selected.
type OpenPRForBranchMsg struct {
	Branch string
}
