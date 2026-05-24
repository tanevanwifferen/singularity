package app

// ConnectionStatus carries transport-layer connection state surfaced to the
// status bar. In remote mode the underlying *client.Client populates this via
// a bubbletea message; in local mode it stays zero-valued.
//
// This file is the surviving slice of the deleted ws.go — transport status is
// the only WebSocket concern that needs to reach the TUI directly (data and
// event streams now flow through service interfaces).
type ConnectionStatus struct {
	Connected    bool
	URL          string
	Error        string
	Reconnecting bool
}

// ConnectionStatusMsg is the tea.Msg variant of ConnectionStatus.
type ConnectionStatusMsg struct {
	Status ConnectionStatus
}
