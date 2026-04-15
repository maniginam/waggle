# Contributing to Waggle

Thanks for your interest in contributing to Waggle! Here's how to get started.

## Getting Started

```bash
git clone https://github.com/maniginam/waggle.git
cd waggle
make build
make test
```

## Development Workflow

1. Fork the repo and create a feature branch from `master`
2. Write tests for your changes
3. Make your changes
4. Run `make test` and ensure all tests pass
5. Submit a pull request

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions small and focused
- Write descriptive commit messages (what and why, not how)
- One logical change per commit

## Testing

All changes should include tests. Run the full suite with:

```bash
make test           # run all tests
make test-verbose   # run with verbose output
make test-cover     # generate coverage report
```

## What to Contribute

- Bug fixes
- Documentation improvements
- New MCP tools
- Dashboard UI improvements
- CLI enhancements
- Performance improvements

## Reporting Issues

Open an issue on GitHub with:
- What you expected to happen
- What actually happened
- Steps to reproduce
- Your environment (OS, Go version)

## Pull Request Guidelines

- Keep PRs focused on a single change
- Update documentation if your change affects the public API or CLI
- Add or update tests as appropriate
- Ensure `make test` passes before submitting

## Architecture

Waggle is a single Go binary with an embedded SQLite database and vanilla JS dashboard. Before making architectural changes, open an issue to discuss the approach.

Key packages:
- `cmd/waggle/` — CLI entry point
- `internal/api/` — REST API handlers
- `internal/server/` — HTTP server, WebSocket hub
- `internal/store/` — SQLite data layer
- `internal/mcp/` — MCP stdio adapter
- `internal/dashboard/` — Embedded web dashboard
- `internal/ws/` — WebSocket connection management
- `internal/push/` — Web push notifications

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
