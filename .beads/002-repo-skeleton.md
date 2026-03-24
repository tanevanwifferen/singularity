---
title: "Bead 002: Repo Skeleton & Tech Decision"
labels: ["task", "singularity", "phase-1"]
priority: "P1"
estimate: 15
---

## Description

Initialize the singularity project with tech stack decision and basic skeleton.

## Task

1. **Research & decide tech stack**: Go+bubbletea vs Rust+ratatui
   - Compare: dev speed, TUI ecosystem, performance, git library support
   - Make a decision and document rationale in `docs/tech-decision.md`
   
2. **Initialize project structure**:
   - Create standard project layout (cmd/, internal/, pkg/ for Go OR src/ for Rust)
   - Set up build system (go.mod + Makefile OR Cargo.toml + build scripts)
   - Create basic "hello world" TUI that renders something (even just "Git Frontend v0.0.1")
   
3. **Initial commit**:
   - Commit all skeleton code with good commit message
   - Tag as v0.0.1-skeleton

## Acceptance Criteria

- [ ] docs/tech-decision.md exists with clear decision and rationale
- [ ] Project builds successfully (make build or cargo build works)
- [ ] Running the binary shows a TUI (even minimal)
- [ ] Git commit with skeleton code
- [ ] Tagged v0.0.1-skeleton

## Context

Project location: `/home/node/code/singularity`
This is bead 002 of 16 in the singularity project.

## Constraints

- Keep it simple - just scaffolding, no real features yet
- Should take <15 minutes
- Use Claude Code with opus model
- All work committed to /home/node/code/singularity
