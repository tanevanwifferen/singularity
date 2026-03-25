# Repo Mode

Repo mode is the default way to use singularity. It opens a TUI for a single git repository and gives you a full set of views for commits, branches, diffs, agents, and git operations.

## Starting

```bash
# Current directory
singularity

# Explicit path
singularity --repo ~/code/my-project
```

## Views

### Overview (`F1`)

The repo health dashboard. Shows at a glance:

- Current branch and HEAD commit
- Uncommitted changes (dirty state)
- Recent commits with author and message
- Stash count and worktree count
- Sync status (ahead/behind upstream)

### Branches (`F2`)

Interactive branch list. Supports:

- `/` to filter by name
- `enter` to checkout
- `d` to delete (with confirmation)
- `c` to compare the selected branch against the current branch
- Local and remote branches

### Commit (`F3`)

Staging area for authoring commits. Workflow:

1. Files with changes appear in the left pane
2. Stage files with `space` or `a` to stage all
3. Press `ctrl+s` to generate a commit message via Claude Code
4. Edit the message if needed, then `enter` to commit

Commit style (conventional, imperative, etc.) is configurable via `ai.commit_style` in `config.json`.

### Log (`F4`)

Scrollable commit history for the current branch.

- `j`/`k` or arrow keys to scroll
- `/` to filter by author or message text
- `enter` to expand a commit and see the full diff

### Agents (`F5`)

Split-pane agent console for interacting with Claude Code.

- Left pane: list of active and past agents
- Right pane: live streaming output for the selected agent
- `n` to start a new agent session
- Type a prompt and press `enter` to send
- `ctrl+c` to kill a running agent
- Follow-up messages can be sent to a running agent

Agents run as Claude Code subprocesses. Each session has:
- A task prompt
- Live structured JSON output
- Cost tracking
- Optional worktree isolation (creates a temp branch, merges back on completion)

### Config (`F6`)

Settings management. Surfaces the main configurable options from `~/.config/singularity/config.json`.

## Git Operations Submenu

Press `g` to open the git operations submenu, then a second key to navigate to it.

### Sync (`g,s`)

Consolidated push/pull interface. Options:

- **Fetch** — update remote refs without merging
- **Pull** — pull with merge or rebase
- **Push** — push to upstream
- **Force Push** — push with `--force-with-lease`
- **Set Upstream** — set the tracking branch

### Branch Compare (`g,b`)

Compare two branches with squash merge awareness.

Select a base branch and a feature branch. The view shows:
- Commits ahead/behind
- File diff statistics
- Whether the feature branch is already merged (including squash-merged PRs, detected by comparing tree content)

### Stashes (`g,t`)

Manage git stashes:

- `n` — create a stash (with optional message)
- `enter` — apply a stash
- `p` — pop a stash (apply and drop)
- `d` — drop a stash (with confirmation)

### Rebase (`g,r`)

Interactive rebase UI. Select a base commit or branch to rebase onto.

Each commit in the list can be set to:
- `p` — pick (keep as-is)
- `r` — reword (edit message)
- `e` — edit (pause for amending)
- `s` — squash (combine with previous)
- `f` — fixup (squash, discard message)
- `d` — drop (remove the commit)

### Worktrees (`g,w`)

Manage git worktrees:

- `n` — create a new worktree (prompts for branch name and path)
- `d` — remove a worktree
- `l` / `u` — lock / unlock a worktree
- `p` — prune stale worktree references

### Pipeline (`g,p`)

CI/CD status dashboard. Shows the latest pipeline run for the current branch on:
- GitHub Actions
- GitLab CI

Requires `gh` or `glab` CLI to be authenticated. Forge is auto-detected from the remote URL.

### Create PR (`g,c`)

MR/PR creation wizard:

1. Picks up current branch and default base automatically
2. Pre-fills title from the last commit message
3. Opens a description editor
4. Lets you select reviewers (if forge API is authenticated)
5. Submits to GitHub or GitLab based on remote URL

### Jira (`g,j`)

Browse and link Jira issues. Only shown when `jira.enabled = true` in config.

## Keybindings

All keybindings can be overridden in `~/.config/singularity/keybinds.json`.

### Global

| Key | Action |
|-----|--------|
| `F1`–`F6` | Switch to primary view |
| `g` | Open git operations submenu |
| `Tab` / `Shift+Tab` | Cycle through views |
| `/` | Filter/search (in list views) |
| `?` | Help overlay |
| `Esc` | Back / cancel |
| `q` / `ctrl+q` | Quit |
| `R` | Refresh current view |

### Jira view (`g,j`)

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate issue list |
| `enter` | Open issue detail pane |
| `/` | Filter issues |
| `s` | Search / JQL query |
| `r` | Refine selected ticket with AI |
| `c` | Create child stories from selected ticket (or from raw text) |
| `w` | Start workflow for selected ticket |
| `R` | Reload issues |
| `Esc` | Close detail / cancel |

### Workflows view (`F5` project mode / `g,w` submenu)

| Key | Action |
|-----|--------|
| `j` / `↓` | Next workflow |
| `k` / `↑` | Previous workflow |
| `w` | New workflow (manual branch name) |
| `J` | New workflow from Jira ticket (picker) |
| `a` | Spawn agent for selected workflow |
| `p` | Push all repos in workflow |
| `M` | Create MRs for all repos in workflow |
| `d` | Open diff view for selected workflow |
| `D` | Delete / clean up selected workflow |
| `I` | Import workflow |
| `r` | Refresh |
| `/` | Filter |

## Configuration Reference

`~/.config/singularity/config.json`:

```json
{
  "theme": {
    "style": "dark",
    "accent_color": "220"
  },
  "git": {
    "default_branch": "main",
    "auto_fetch": true,
    "fetch_interval": 60,
    "max_branch_depth": 50,
    "show_remote_branches": true
  },
  "forge": {
    "default_host": "github.com",
    "api_url": "",
    "token": "",
    "auto_assign_me": true
  },
  "ai": {
    "provider": "claude",
    "model": "claude-3-5-sonnet",
    "commit_style": "conventional",
    "max_tokens": 1024
  },
  "jira": {
    "enabled": false,
    "base_url": "https://company.atlassian.net",
    "email": "user@company.com",
    "api_token": "",
    "default_project": "PROJ"
  }
}
```
