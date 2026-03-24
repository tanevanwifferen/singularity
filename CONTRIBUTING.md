# Contributing

## Dev Setup

After cloning, enable the git hooks:

```bash
git config core.hooksPath .githooks
```

This installs a pre-commit hook that auto-runs `go fmt` on staged Go files before each commit.

## Useful Commands

```bash
make build   # Compile
make test    # Run tests
make fmt     # Format code
make tidy    # go mod tidy
```
