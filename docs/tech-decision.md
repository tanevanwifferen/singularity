# Tech Stack Decision: Go + Bubbletea

**Date:** 2026-03-19  
**Decision:** Go with Bubbletea TUI framework  
**Status:** Final

## Summary

After evaluating Go+bubbletea vs Rust+ratatui for the Git Frontend project, we chose **Go** as the primary language with **bubbletea** as the TUI framework.

## Criteria Comparison

| Criteria | Go + Bubbletea | Rust + Ratatui | Winner |
|----------|---------------|----------------|--------|
| **Dev Speed** | Fast compilation, simple tooling, less boilerplate | Slower compilation, more verbose, complex borrow checker | Go |
| **TUI Ecosystem** | bubbletea is mature, simple model-based architecture | ratatui is excellent, more low-level control | Tie |
| **Performance** | Good for TUI, binary ~5-10MB | Excellent, binary ~2-5MB, lower memory | Rust |
| **Git Library** | go-git (pure Go, actively maintained) | git2-rs (libgit2 bindings, C FFI) | Tie |
| **CLI Integration** | `os/exec` is simple and idiomatic | `std::process` works but more verbose | Go |
| **AI/HTTP Clients** | Excellent stdlib `net/http`, easy JSON | reqwest is great but adds dependencies | Go |
| **Cross-platform** | Excellent, single binary | Good, but more build complexity | Go |
| **Learning Curve** | Gentle, productive day 1 | Steep, especially for TUI ergonomics | Go |

## Decision Rationale

### Why Go?

1. **Iteration Speed Matters Most** - This is a new project with uncertain requirements. Go's fast compile times (sub-second for small changes) enable rapid experimentation.

2. **Bubbletea's Model-View-Update** - The Elm architecture in bubbletea is perfect for TUI state management. Commands and messages are explicit, making complex git operations easier to reason about.

3. **Git Operations are I/O Bound** - Most git operations involve disk I/O and subprocess calls. Go's goroutines handle this naturally without Rust's async complexity.

4. **Simpler Subprocess Management** - Spawning `gh`, `glab`, and `claude` subprocesses is straightforward with `os/exec`.

5. **Good Enough Performance** - TUI responsiveness depends more on algorithm choice than language. Go is plenty fast for rendering terminal UI at 60fps.

### Why Not Rust?

Rust is an excellent choice for many things, but for this specific project:

- The safety benefits are marginal (git operations are inherently safe with proper error handling)
- The performance gains don't justify the development time cost
- libgit2 bindings add C dependency complexity
- Async Rust for subprocess management is overkill

## Project Structure

```
git-frontend/
├── cmd/
│   └── git-frontend/
│       └── main.go          # Entry point
├── internal/
│   ├── app/                 # Bubbletea app state & logic
│   ├── git/                 # Git operations wrapper
│   ├── forge/               # GitHub/GitLab API clients
│   ├── tui/                 # TUI components & rendering
│   └── config/              # Configuration management
├── pkg/                     # Public libraries (if any)
├── docs/
│   └── tech-decision.md     # This file
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/lipgloss` - Styling
- `github.com/go-git/go-git/v5` - Git operations
- `github.com/charmbracelet/gum` - CLI helpers (optional)

## Future Considerations

If performance becomes a bottleneck, we can:
1. Profile and optimize hot paths first
2. Consider Rust for specific performance-critical modules
3. Rewrite to Rust only if metrics show it's necessary

## Conclusion

Go + bubbletea provides the best balance of development speed, maintainability, and performance for this project. We can always optimize or rewrite later if needed.

---

*Decision made by: Asina Papi*  
*Approved by: Tane*
