# Git Frontend - Project Specification

## Problem Statement

Current git frontends (VS Code, GitKraken, etc.) fail at:

1. **Squash merge comparison**: They show branches as diverged even after squash merge because they compare commit SHAs, not actual tree content
2. **Shallow integration**: GitHub/GitLab features are afterthoughts, not core
3. **No AI automation**: Commit messages, PR descriptions, code review all manual
4. **Editor bloat**: We don't need another editor—we need a git command center

## Solution

A TUI-first git frontend that:

### Core Features

1. **Smart Branch Comparison**
   - Compare branches by tree content, not commit history
   - Detect squash merges correctly
   - Show "effective divergence" vs "commit divergence"
   - Visual diff of what would actually change on merge

2. **Automated Commit Workflows**
   - AI-generated commit messages from staged changes
   - Commit message templates per project
   - Auto-squash suggestions for related commits
   - Interactive rebase with AI-powered reword

3. **Forge Integration (GitLab/GitHub)**
   - Create MRs/PRs from TUI
   - View CI/CD pipeline status
   - Manage issues linked to branches
   - Review comments inline
   - Merge requests with automated checks

4. **Claude Code Integration**
   - Embedded sessions per-project
   - "Fix this" → spawns Claude Code in background
   - Code review on staged changes
   - Refactoring suggestions before commit

5. **Advanced Git Operations**
   - Interactive rebase visualization
   - Bisect with AI-powered bug localization
   - Stash management with previews
   - Worktree management
   - Submodule dashboard

### Non-Goals

- File editing (use vim/nvim/VS Code for that)
- Terminal emulation
- Full IDE features
- Language server protocol (LSP)

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    TUI Layer                        │
│  (bubbletea/go or ratatui/rust)                     │
├─────────────────────────────────────────────────────┤
│               Command & State Layer                 │
│  - Git operations (custom + libgit2)                │
│  - Forge API clients (glab/gh wrappers)             │
│  - Claude Code session manager                      │
│  - AI commit message generator                      │
├─────────────────────────────────────────────────────┤
│                  Storage Layer                      │
│  - Local git repo                                   │
│  - Config (~/.config/git-frontend/)                 │
│  - Cache (CI status, MR state)                      │
└─────────────────────────────────────────────────────┘
```

## Success Criteria

- [ ] Can replace VS Code for all git operations
- [ ] Correctly handles squash merge scenarios
- [ ] Creates MRs/PRs without leaving TUI
- [ ] Generates commit messages that need no editing
- [ ] Shows CI status for branches at a glance
- [ ] Feels fast (<100ms for most operations)

## Open Questions

1. **TUI vs Electron**: Start TUI, port to Electron later? Or build both from start?
2. **Language**: Go (bubbletea, fast dev) vs Rust (ratatui, performance, safety)?
3. **Custom git implementation**: How much to implement vs libgit2 bindings?
4. **Claude Code integration**: WebSocket sessions? Sub-agent spawns per-action?

## Risks

- **Timeout issues**: Long-running Claude Code sessions might timeout
  - Mitigation: Break tasks into <15min beads, use sub-agents with proper timeout handling
- **Scope creep**: Easy to turn this into "another VS Code"
  - Mitigation: Strict adherence to "git command center only"
- **TUI limitations**: Complex visualizations hard in TUI
  - Mitigation: Start simple, iterate; consider hybrid TUI + browser for complex views

---

_Next: Break into project beads for implementation_
