# Contributing

## Dev Setup

After cloning, install the git hooks:

```bash
make setup
```

This copies the pre-commit hook into `.git/hooks/`, which is shared across all worktrees. It auto-runs `go fmt` on staged Go files before each commit.

## Useful Commands

```bash
make build   # Compile
make test    # Run tests
make fmt     # Format code
make tidy    # go mod tidy
```
