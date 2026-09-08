package engine

// UnattendedSessionCommand delegates to pi. The interface contract (see
// backend.go) wants one exec that runs a tool-enabled session to completion
// without ever blocking on input. herdr's value is the opposite of that — it
// drives claude's interactive TUI in a pane precisely because `claude --print`
// is refused on a Max-plan subscription — so nothing behind Binary()/Args()
// fits the unattended, single-exec shape. pi does: it has no permission
// prompts and its print mode carries no Max-plan restriction (see
// backend_pi_session.go), so the rebase-conflict resolution in worktree.go
// keeps working under `ai.provider: herdr` instead of failing loudly.
func (b *herdrBackend) UnattendedSessionCommand(prompt string) (string, []string, error) {
	return NewPiBackend("").UnattendedSessionCommand(prompt)
}
