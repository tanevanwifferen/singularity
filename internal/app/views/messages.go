package views

// RefreshMsg is a request to refresh the view data
type RefreshMsg struct{}

// OpenPRForBranchMsg requests the PR/MR creation view to open with a specific source branch pre-selected.
type OpenPRForBranchMsg struct {
	Branch string
}

// OpenAgentMsg requests the Agents view to open with a specific agent
// selected. The flows view sends it: a flow tree's step names the agent that
// ran it, and the transcript lives in the Agents view rather than being
// duplicated into the tree pane.
type OpenAgentMsg struct {
	AgentID string
}
