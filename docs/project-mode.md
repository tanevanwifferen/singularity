# Project Mode

Project mode lets you manage a set of related git repositories as a single unit. It's designed for workflows where a feature, deployment, or release spans multiple repos — microservices, monorepos split into sub-repos, frontend/backend pairs, etc.

## When to Use Project Mode

- You regularly work across 2+ repos in tandem (e.g. an API and its consumer)
- You want to check whether a feature branch exists in all repos before merging
- You want to run agents with shared context across repos (e.g. architecture docs)
- You want a single dashboard showing dirty state and branch health across all repos

## Starting

```bash
# Explicit config file
singularity --project-config ~/.config/singularity/projects.json

# Auto-discovered (default path if file exists)
singularity
```

## Setting Up a Project

Projects are defined in `~/.config/singularity/projects.json`.

### Manual Config

```json
{
  "projects": {
    "my-platform": {
      "name": "My Platform",
      "repos": [
        {
          "name": "api",
          "path": "~/code/my-platform/api",
          "default_branch": "main"
        },
        {
          "name": "web",
          "path": "~/code/my-platform/web",
          "default_branch": "main"
        },
        {
          "name": "infra",
          "path": "~/code/my-platform/infra",
          "default_branch": "main"
        }
      ],
      "context_files": [
        "README.md",
        "docs/architecture.md",
        "docs/api-contracts.md"
      ]
    }
  }
}
```

### Auto-Generate from Directory

Scan a directory tree for git repos and print a ready-to-use config:

```bash
# Print to stdout — review before saving
singularity --generate-config-from-dir ~/code/my-platform

# Scan current directory and add directly to projects config
singularity --init
```

`--generate-config-from-dir` outputs valid JSON to stdout so you can pipe it, edit it, then save it:

```bash
singularity --generate-config-from-dir ~/code/my-platform \
  | jq '.projects["my-platform"].context_files = ["README.md"]' \
  > ~/.config/singularity/projects.json
```

## Views

### Project (`F1`)

The cross-repo dashboard. Shows all repos in the project at once.

**Left panel** — repo list with aggregate status:
- Green: clean, in sync
- Yellow: uncommitted changes
- Red: error or conflict state

**Right panel** — expandable branch tree for the selected repo:
- Current branch highlighted
- Remote tracking branch and ahead/behind counts
- Branch existence check across repos (e.g. "is `feature/payments` in all repos?")

**Actions:**
- `enter` — expand/collapse repo details
- `r` — refresh all repos
- `b` — check whether the current branch exists across all repos
- `[` / `]` — cycle through repos

### Workflows (`F2`)

Feature workflow management for multi-repo operations.

Use this view to:
- Create a feature branch across all repos simultaneously
- Push the same branch to remotes in all repos
- Open MRs/PRs in all repos for a given branch
- Run an agent with the full project context

Workflows understand that some repos might already be on the branch (idempotent creation) and report per-repo status individually.

### Agents (`F3`)

Same agent console as repo mode, but with project context automatically injected.

When you start an agent in project mode, it receives:
- The paths to all repos in the project
- The content of any `context_files` listed in the project config
- A summary of current branch state across repos

This means you can ask questions like:
- "Does our API schema match the TypeScript types in the web repo?"
- "Which repos need to be updated for this feature to work end to end?"

## Repo Navigation

In project mode, `[` and `]` cycle through the repos in the project. The selected repo is used as context in the Workflows view and determines which branch tree is shown in the Project view.

## Context Files

The `context_files` field in a project config lists paths — **relative to each repo root** — that should be read and injected into agent sessions.

```json
"context_files": [
  "README.md",
  "docs/architecture.md",
  "openapi.yaml"
]
```

Files that don't exist in a repo are silently skipped, so you can list files that only some repos have.

Use context files for:
- Architecture docs that agents need to understand the system
- API contracts or schema files
- Contribution guidelines or coding standards

## Cross-Repo Branch Check

Project mode can check whether a branch exists in all (or specific) repos. From the Project view, press `b` to run a branch check against the current branch across every repo in the project.

The result shows a per-repo table:
- Branch exists locally
- Branch exists on remote
- Ahead/behind relative to the repo's default branch

This is useful before merging or deploying to ensure all repos are ready.

## Project Config Reference

```json
{
  "projects": {
    "<project-key>": {
      "name": "Display Name",
      "repos": [
        {
          "name": "short-name",
          "path": "/absolute/or/~/relative/path",
          "default_branch": "main"
        }
      ],
      "context_files": [
        "path/relative/to/each/repo/root"
      ]
    }
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Display name shown in the TUI |
| `repos` | array | List of repositories in the project |
| `repos[].name` | string | Short label for the repo (shown in UI) |
| `repos[].path` | string | Absolute path or `~/`-prefixed home-relative path |
| `repos[].default_branch` | string | Branch used as base for comparisons (default: `main`) |
| `context_files` | array | Files to inject into agent sessions (relative to each repo root) |

## Compared to Repo Mode

| Feature | Repo Mode | Project Mode |
|---------|-----------|--------------|
| Single repo | Yes | No (manages many) |
| Multi-repo dashboard | No | Yes |
| Cross-repo branch check | No | Yes |
| Agent context injection | Repo-scoped | Project-wide context files |
| Branch operations | Full | Via Workflows view |
| Git submenu (`g`) | Full | Not available (use Workflows) |
| Stash / rebase / worktrees | Yes | Open a repo in repo mode |

For deep single-repo work (interactive rebase, stash management, etc.) within a project, open that repo directly with `singularity --repo <path>`.
