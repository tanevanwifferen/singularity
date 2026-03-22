# Git Frontend TUI Improvement Plan

## 1. Current State Analysis

### What Exists
The TUI currently has two minimal views built with Bubbletea + Lipgloss:

- **Main App View** (`internal/app/app.go`): Static display of repo info — current branch, HEAD, dirty status, remotes list, branches list with ahead/behind counts. Keybinds: `q` quit, `r` refresh.
- **Branch Dashboard** (`internal/app/dashboard.go`): Interactive branch list with j/k navigation, enter to compare selected branch with current (shows ahead/behind + tree comparison with squash detection). Keybinds: up/down, enter, esc, q.

### What's Missing
The TUI barely scratches the surface of the backend. The `internal/git/` package already implements:
- **Diffs**: file-level diffs between branches with additions/deletions per file
- **Rebase**: interactive rebase planning, commit reordering, squash, message editing
- **Stash**: list/create/apply/drop stash entries
- **Worktrees**: list/create/remove/lock/unlock worktrees
- **Commit messages**: AI-generated conventional commit messages from staged diffs
- **Forge integration**: GitHub PR / GitLab MR creation, auth detection
- **Pipeline/CI**: GitHub Actions / GitLab CI status per branch
- **Multi-repo projects**: cross-repo branch checking, aggregate status
- **Agent pool**: Claude Code agent lifecycle management with streaming output
- **Server/client**: full HTTP/WebSocket API (20+ endpoints)

The TUI also lacks fundamental UX patterns:
- No view/tab navigation system
- No terminal resize handling in the main app
- No scrolling for long lists
- No search/filter
- No confirmation dialogs
- No loading states for async operations
- No help overlay
- Dashboard component isn't wired into the main app (two separate entry points)

---

## 2. Proposed Views

### View 1: Repository Overview (Home)
**Purpose**: Landing page showing repo health at a glance.
- Current branch, HEAD, dirty status
- Recent commits (last 5-10) with short hash + subject
- Upstream sync status (ahead/behind remote)
- Active stash count
- CI/pipeline status for current branch
- Worktree count
- Keybinds: navigate to other views

### View 2: Branch Explorer
**Purpose**: Browse, compare, and manage branches.
- Scrollable branch list with search/filter
- Inline ahead/behind + CI status badges per branch
- Branch comparison panel (split or overlay)
- File diff summary when comparing
- Actions: checkout, delete, create, merge, rebase onto
- Sort by: name, last commit date, ahead/behind

### View 3: Diff Viewer
**Purpose**: View file-level and line-level diffs between branches or HEAD.
- File tree on left, diff content on right (split pane)
- File status indicators (Added/Modified/Deleted)
- Additions/deletions counts per file
- Scrollable diff with syntax-aware coloring
- Toggle between staged, unstaged, and branch-vs-branch diffs

### View 4: Commit Composer
**Purpose**: Stage changes and create commits with AI assistance.
- File list with staged/unstaged toggle (space to stage/unstage)
- Diff preview for selected file
- AI-generated commit message (invoke with keybind)
- Editable message field with conventional commit format
- Commit action with confirmation

### View 5: Stash Manager
**Purpose**: Manage stash entries.
- Stash list with message, date, branch context
- Preview stash contents (file list)
- Actions: apply, pop, drop, create new stash
- Confirmation dialog for destructive actions

### View 6: Rebase Planner
**Purpose**: Visual interactive rebase.
- Commit list with drag-to-reorder (j/k + shift to move)
- Per-commit operation selector: pick, reword, edit, squash, fixup, drop
- Live preview of rebase plan (todo list format)
- Execute rebase with progress indicator
- Abort/continue/skip controls during active rebase

### View 7: Worktree Manager
**Purpose**: Manage git worktrees.
- Worktree list with branch, HEAD, path, lock status
- Actions: create (new or existing branch), remove, lock/unlock, prune
- Navigate to worktree path

### View 8: PR/MR Creator
**Purpose**: Create pull requests / merge requests.
- Source/target branch selection
- Auto-generated title from branch comparison
- Description editor (multi-line text input)
- Reviewer selection (if available)
- Forge auto-detection (GitHub/GitLab)
- Submit with status feedback

### View 9: CI/Pipeline Dashboard
**Purpose**: Monitor CI/CD status across branches.
- Branch list with pipeline status icons
- Pipeline detail: jobs list with status, duration
- Retry failed pipelines
- Auto-refresh with configurable interval

### View 10: Agent Console (Server Mode)
**Purpose**: Monitor and manage Claude Code agents.
- Agent list with status (idle/running/complete/error)
- Live streaming output for running agents
- Start new agent tasks
- Kill/timeout controls
- Session history

### View 11: Project Overview (Multi-Repo)
**Purpose**: Aggregate view across multiple repositories.
- Repo list with status (clean/dirty/errors)
- Cross-repo branch existence check
- Per-repo quick stats
- Navigate into single-repo views

---

## 3. UX Improvements

### Navigation System
- **Tab bar** at top showing available views with highlighted active view
- **Number keys (1-9)** for direct view switching
- **Tab/Shift+Tab** to cycle between views
- **Breadcrumb** showing current context: `repo > branches > compare`
- **`?`** opens help overlay with all keybinds for current view

### Layout Framework
- **Responsive layout** that adapts to terminal width/height
- **Split pane support** for side-by-side views (diff viewer, branch compare)
- **Overlay/modal system** for confirmations, help, quick actions
- **Status bar** at bottom: repo name, current branch, dirty indicator, view name

### List Interactions
- **Scrolling** with viewport management for all lists
- **Search/filter** with `/` to start typing, `Esc` to clear
- **Multi-select** with `v` for batch operations (e.g., delete multiple branches)
- **Preview pane** that updates as cursor moves

### Feedback & State
- **Loading spinners** for git operations (spinner component from Bubbles)
- **Toast notifications** for success/error messages (auto-dismiss)
- **Confirmation dialogs** for destructive operations
- **Progress bars** for long operations (rebase, clone)

### Color & Theme
- **Consistent color palette** defined in a central theme
- **Semantic colors**: green=added, red=removed, yellow=modified, blue=info
- **Bold/dim** for hierarchy (headers bold, secondary info dim)
- **Optional dark/light theme toggle**

### Keyboard-First Design
- **Vim-style navigation** throughout (j/k/h/l, g/G for top/bottom)
- **Command palette** with `:` (like vim) for less-common actions
- **Consistent keybinds** across views (q=back, Q=quit, r=refresh, ?=help)

---

## 4. Implementation Phases

### Phase 0: Foundation (Prerequisites)
Build the infrastructure that all views depend on.

### Phase 1: Core Views
Implement the most essential views that cover daily git workflow.

### Phase 2: Advanced Git Operations
Add views for power-user git features.

### Phase 3: Integrations
Connect forge, CI, and agent capabilities.

### Phase 4: Multi-Repo & Polish
Project management and final UX refinements.

---

## 5. Bead-Sized Tasks

Each task is designed to be implementable in 1-2 hours. Tasks are ordered by dependency — earlier tasks are prerequisites for later ones.

### Phase 0: Foundation

#### 0.1 — Create view routing system
**File**: `internal/app/router.go` (new)
- Define `View` interface with `Init()`, `Update()`, `View()`, `ShortHelp()` methods
- Create `Router` struct that holds active view + view registry
- Implement view switching by name/index
- Wire into main `Model` so `Update` and `View` delegate to active view
- **Acceptance**: Can switch between two stub views with number keys

#### 0.2 — Create shared layout components
**File**: `internal/app/layout.go` (new)
- Tab bar component: renders view names, highlights active
- Status bar component: repo name, branch, dirty indicator, view name
- Main `Model.View()` composes: tab bar + active view + status bar
- Handle `WindowSizeMsg` to pass available width/height to active view
- **Acceptance**: Tab bar and status bar render correctly at various terminal sizes

#### 0.3 — Create theme/style system
**File**: `internal/app/theme.go` (new)
- Central `Theme` struct with named lipgloss styles
- Semantic color constants (Added, Removed, Modified, Info, Warning, Error)
- Header, body, footer, accent style presets
- Migrate existing hardcoded styles in `app.go` and `dashboard.go` to use theme
- **Acceptance**: All existing views use theme styles, changing theme changes all views

#### 0.4 — Create reusable list component with scrolling
**File**: `internal/app/components/list.go` (new)
- Generic scrollable list with viewport management
- Cursor tracking, page up/down, home/end
- Configurable item renderer (callback)
- Selected item highlighting
- Item count display
- **Acceptance**: Can scroll a 100-item list in a 20-line viewport

#### 0.5 — Create modal/overlay system
**File**: `internal/app/components/modal.go` (new)
- Modal overlay that renders on top of current view
- Confirmation dialog (yes/no with callback)
- Info/error message dialog (auto-dismiss or press key)
- Help overlay (renders keybind table for current view)
- **Acceptance**: Can show a confirmation dialog and capture yes/no response

#### 0.6 — Create search/filter component
**File**: `internal/app/components/filter.go` (new)
- Inline text input activated by `/`
- Filters a list in real-time as user types
- `Esc` clears filter, `Enter` confirms (keeps filter active)
- Case-insensitive substring match
- **Acceptance**: Can filter a branch list by typing partial branch name

#### 0.7 — Create loading spinner component
**File**: `internal/app/components/spinner.go` (new)
- Wraps Bubbles spinner for consistent styling
- Can overlay on any view during async operations
- Message text next to spinner
- **Acceptance**: Spinner displays during repo refresh

#### 0.8 — Integrate dashboard into main app as a view
**File**: `internal/app/app.go`, `internal/app/dashboard.go`
- Refactor `BranchDashboard` to implement `View` interface
- Register as a view in the router
- Remove standalone `BranchDashboard` entry point
- Unify the two current models into one app with views
- **Acceptance**: App launches with overview, can switch to branch dashboard view

### Phase 1: Core Views

#### 1.1 — Build Repository Overview view
**File**: `internal/app/views/overview.go` (new)
- Show current branch, HEAD (short), dirty status
- List last 5 commits (shell out to `git log --oneline -5`)
- Show remote sync status
- Show stash count, worktree count
- Keybinds: `r` refresh, number keys to jump to other views
- **Acceptance**: Overview renders all sections with live repo data

#### 1.2 — Rebuild Branch Explorer with new list component
**File**: `internal/app/views/branches.go` (new)
- Use reusable list component for branch listing
- Add `/` search/filter
- Show ahead/behind inline with color coding
- Show upstream tracking info
- `Enter` opens comparison panel
- **Acceptance**: Can filter branches, scroll, and compare

#### 1.3 — Add branch comparison split panel
**File**: `internal/app/views/branches.go`
- When comparing, split view: branch list left, comparison right
- Show ahead/behind commit counts
- Show tree comparison + squash detection
- Show file diff summary (files changed, additions, deletions)
- `Esc` closes comparison panel
- **Acceptance**: Split panel shows diff summary for selected branch pair

#### 1.4 — Build Diff Viewer view
**File**: `internal/app/views/diff.go` (new)
- Accept branch pair or show staged/unstaged changes
- File list on left with status indicators (A/M/D)
- Per-file additions/deletions count
- Scrollable file list with cursor
- Detail panel shows file path, status, change counts
- **Acceptance**: Can browse changed files between two branches

#### 1.5 — Add diff content rendering
**File**: `internal/app/views/diff.go`
- Shell out to `git diff` for selected file
- Render unified diff with +/- coloring (green/red)
- Line numbers in gutter
- Scrollable diff content viewport
- **Acceptance**: Can view colored line-level diff for any changed file

#### 1.6 — Build Commit Composer view
**File**: `internal/app/views/commit.go` (new)
- List files with staged/unstaged status
- `Space` to toggle stage/unstage individual files
- `a` to stage all, `u` to unstage all
- Show staged diff summary (files, additions, deletions)
- **Acceptance**: Can stage/unstage files interactively

#### 1.7 — Add commit message editor with AI generation
**File**: `internal/app/views/commit.go`
- Text input for commit message (multi-line)
- `Ctrl+G` to generate AI commit message from staged diff
- Show generated message, allow editing
- `Ctrl+Enter` to commit with confirmation
- Show success/error result
- **Acceptance**: Can generate AI message, edit it, and commit

#### 1.8 — Build Help overlay
**File**: `internal/app/components/help.go` (new)
- Each view exposes `KeyBindings() []KeyBinding` (name + description)
- `?` toggles help overlay showing all bindings for current view
- Global bindings section + view-specific section
- Scrollable if many bindings
- **Acceptance**: Help overlay shows correct bindings per view

### Phase 2: Advanced Git Operations

#### 2.1 — Build Stash Manager view
**File**: `internal/app/views/stash.go` (new)
- List stash entries with index, message, branch
- Cursor navigation with preview of stash file list
- `a` apply, `p` pop (apply + drop), `d` drop with confirmation
- `n` create new stash (text input for message, toggle untracked)
- `D` clear all stashes with confirmation
- **Acceptance**: Can list, create, apply, and drop stashes

#### 2.2 — Build Rebase Planner view
**File**: `internal/app/views/rebase.go` (new)
- Input: select base branch to rebase onto
- Show commit list from rebase plan
- Cursor on commits, `o` to cycle operation (pick/reword/squash/fixup/drop)
- `K`/`J` (shift+k/j) to move commit up/down
- Preview todo list in side panel
- **Acceptance**: Can build a rebase plan with reordered commits and varied operations

#### 2.3 — Add rebase execution and progress
**File**: `internal/app/views/rebase.go`
- `Enter` to execute rebase with confirmation
- Show progress/status during rebase
- Conflict detection with abort/continue/skip controls
- Status bar shows "REBASE IN PROGRESS" when active
- **Acceptance**: Can execute a rebase and handle conflicts

#### 2.4 — Build Worktree Manager view
**File**: `internal/app/views/worktree.go` (new)
- List worktrees with path, branch, HEAD, lock status
- `n` create new worktree (path input + branch selection)
- `d` remove worktree with confirmation (force option)
- `l`/`u` lock/unlock
- `p` prune stale worktrees
- **Acceptance**: Can list, create, remove, and lock/unlock worktrees

#### 2.5 — Add commit log view
**File**: `internal/app/views/log.go` (new)
- Scrollable commit log with hash, author, date, subject
- Configurable depth (default 50, load more on scroll)
- Filter by author or message text
- `Enter` to show commit detail (full message, changed files)
- **Acceptance**: Can browse commit history with filtering

### Phase 3: Integrations

#### 3.1 — Build CI/Pipeline Dashboard view
**File**: `internal/app/views/pipeline.go` (new)
- Show pipeline status for current branch prominently
- List branches with their CI status icons
- Detail panel: jobs list with status, duration
- `R` retry failed pipeline
- Auto-refresh toggle with interval
- **Acceptance**: Can view CI status for branches and retry pipelines

#### 3.2 — Add CI status badges to Branch Explorer
**File**: `internal/app/views/branches.go`
- Fetch pipeline status in background for visible branches
- Show status icon (✓/✗/●/◌) next to each branch
- Color: green=passed, red=failed, yellow=running, grey=pending
- Cache results to avoid API spam
- **Acceptance**: Branch list shows CI status inline

#### 3.3 — Build PR/MR Creator view
**File**: `internal/app/views/pr.go` (new)
- Source branch (default: current) and target branch selection
- Auto-generated title from `GenerateMRTitle`
- Multi-line description editor
- Forge auto-detection with status display
- Submit button with loading state and result URL display
- **Acceptance**: Can create a GitHub PR or GitLab MR from the TUI

#### 3.4 — Build Agent Console view (server mode only)
**File**: `internal/app/views/agent.go` (new)
- List agents with status, task description, elapsed time
- Select agent to view streaming output (scrollable viewport)
- `n` start new agent (task input)
- `k` kill selected agent
- Status indicators: idle=grey, running=blue, complete=green, error=red
- **Acceptance**: Can view agent status and output in real-time

#### 3.5 — Add WebSocket client integration for live updates
**File**: `internal/app/ws.go` (new)
- Connect to server WebSocket when in client mode
- Receive events: repo_update, branch_update, pipeline_update, agent_output
- Dispatch events to relevant views for live refresh
- Reconnection logic with backoff
- **Acceptance**: TUI auto-updates when server pushes events

### Phase 4: Multi-Repo & Polish

#### 4.1 — Build Project Overview view (multi-repo)
**File**: `internal/app/views/project.go` (new)
- List repos in project with status (clean/dirty/error)
- Per-repo: current branch, dirty indicator, error message
- `Enter` to navigate into single-repo mode for selected repo
- Cross-repo branch check (does branch X exist in all repos?)
- **Acceptance**: Can view aggregate project status and drill into repos

#### 4.2 — Add command palette
**File**: `internal/app/components/palette.go` (new)
- `:` opens command input at bottom of screen
- Fuzzy-match command list as user types
- Commands: switch-view, refresh, checkout, create-branch, etc.
- `Esc` to dismiss, `Enter` to execute
- **Acceptance**: Can execute any registered command via palette

#### 4.3 — Add notification/toast system
**File**: `internal/app/components/toast.go` (new)
- Toast messages appear briefly at bottom-right
- Types: success (green), error (red), info (blue), warning (yellow)
- Auto-dismiss after 3 seconds or press any key
- Queue multiple toasts
- **Acceptance**: Operations show success/error toasts

#### 4.4 — Add keyboard shortcut customization
**File**: `internal/app/keybinds.go` (new)
- Default keybind map defined in code
- Optional JSON config file (`~/.config/git-frontend/keybinds.json`)
- Config overrides defaults
- Help overlay reads from resolved keybind map
- **Acceptance**: User can remap keys via config file

#### 4.5 — Add mouse support
**File**: `internal/app/app.go`
- Enable Bubbletea mouse support
- Click to select list items
- Click tab bar to switch views
- Scroll wheel for viewport scrolling
- **Acceptance**: Can navigate basic UI with mouse

#### 4.6 — Performance: async git operations
**File**: `internal/app/async.go` (new)
- Run git commands in goroutines, send results as tea.Msg
- Show spinner during loading
- Cancel in-flight operations on view switch
- Debounce rapid refresh requests
- **Acceptance**: UI stays responsive during slow git operations

#### 4.7 — Add configuration system
**File**: `internal/config/config.go` (extend)
- YAML/JSON config: `~/.config/git-frontend/config.json`
- Options: theme (dark/light), default view, refresh interval, max log depth
- CLI flags override config file
- **Acceptance**: App reads config on startup and applies settings

#### 4.8 — Integration tests for view routing
**File**: `internal/app/router_test.go` (new)
- Test view switching
- Test keybind dispatch to correct view
- Test layout rendering at various terminal sizes
- Test modal overlay interaction
- **Acceptance**: All routing and layout tests pass

---

## Summary

| Phase | Tasks | Focus |
|-------|-------|-------|
| 0: Foundation | 8 tasks | View system, components, theme |
| 1: Core Views | 8 tasks | Overview, branches, diff, commit |
| 2: Advanced | 5 tasks | Stash, rebase, worktrees, log |
| 3: Integrations | 5 tasks | CI, PR/MR, agents, WebSocket |
| 4: Polish | 8 tasks | Multi-repo, palette, config, tests |
| **Total** | **34 tasks** | |

### Recommended Starting Order
1. **0.1** (router) → **0.2** (layout) → **0.3** (theme) — unlocks everything
2. **0.4** (list) → **0.8** (integrate dashboard) — proves the system works
3. **1.1** (overview) → **1.2** (branches) — core daily workflow
4. Then proceed through phases in order, parallelizing independent tasks within each phase
