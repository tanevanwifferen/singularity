package engine

// UnattendedSessionCommand runs a full pi session in print mode.
//
// pi has no equivalent of claude's --permission-mode bypassPermissions, because
// pi has no tool-permission prompts to bypass. Its design principles state that
// it "intentionally does not include built-in MCP, sub-agents, permission
// popups, plan mode, to-dos, or background bash" (docs/usage.md), and its
// built-in tools (read, bash, powershell, edit, write, grep, find, ls) run
// without confirmation.
//
// The only interactive gate pi has is the project-trust prompt, and two things
// keep it from ever firing here:
//
//   - Print mode installs a no-op UI context in which every dialog method
//     resolves immediately (confirm → false, select → undefined), so no
//     extension can block the session waiting for a human.
//   - --no-approve sets the project-trust override, which is the first return
//     in pi's trust resolution — ahead of the trust store, the "ask" default and
//     any extension project_trust handler. It also keeps project-local
//     extensions and .pi/settings.json out of the run, so a repository being
//     rebased cannot inject configuration into an unattended session. The
//     trade-off is that project-local pi skills and settings are ignored during
//     conflict resolution, which is the right side to err on here.
//
// --no-session keeps the run ephemeral, matching Args(). "--" ends option
// parsing so a prompt beginning with a dash is still treated as the message;
// note that pi also reads a leading "@" as a file reference, so the prompt
// passed here must not start with one.
func (b *piBackend) UnattendedSessionCommand(prompt string) (string, []string, error) {
	return "pi", []string{
		"--print",
		"--no-session",
		"--no-approve",
		"--",
		prompt,
	}, nil
}
